package main

import (
	"debug/dwarf"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"debug/gosym"
	"fmt"
	"os"
)

type Section interface {
	Data() ([]byte, error)
}

type Executable struct {
	Data      *dwarf.Data
	TextStart uint64
	Text      []byte
	Gosym *gosym.Table
}

type openFn func(string) (dwarfData *dwarf.Data, textStart uint64, text Section, goSymboltable *gosym.Table)

// Borrowed from https://golang.org/src/cmd/internal/objfile/pe.go
func pclnPE(exe *pe.File) (textStart uint64, symtab, pclntab []byte, err error) {
	var imageBase uint64
	switch oh := exe.OptionalHeader.(type) {
	case *pe.OptionalHeader32:
		imageBase = uint64(oh.ImageBase)
	case *pe.OptionalHeader64:
		imageBase = oh.ImageBase
	default:
		return 0, nil, nil, fmt.Errorf("pe file format not recognized")
	}
	if sect := exe.Section(".text"); sect != nil {
		textStart = imageBase + uint64(sect.VirtualAddress)
	}
	if pclntab, err = loadPETable(exe, "runtime.pclntab", "runtime.epclntab"); err != nil {
		// We didn't find the symbols, so look for the names used in 1.3 and earlier.
		// TODO: Remove code looking for the old symbols when we no longer care about 1.3.
		var err2 error
		if pclntab, err2 = loadPETable(exe, "pclntab", "epclntab"); err2 != nil {
			return 0, nil, nil, err
		}
	}
	if symtab, err = loadPETable(exe, "runtime.symtab", "runtime.esymtab"); err != nil {
		// Same as above.
		var err2 error
		if symtab, err2 = loadPETable(exe, "symtab", "esymtab"); err2 != nil {
			return 0, nil, nil, err
		}
	}
	return textStart, symtab, pclntab, nil
}

// Borrowed from https://golang.org/src/cmd/internal/objfile/pe.go
func loadPETable(f *pe.File, sname, ename string) ([]byte, error) {
	ssym, err := findPESymbol(f, sname)
	if err != nil {
		return nil, err
	}
	esym, err := findPESymbol(f, ename)
	if err != nil {
		return nil, err
	}
	if ssym.SectionNumber != esym.SectionNumber {
		return nil, fmt.Errorf("%s and %s symbols must be in the same section", sname, ename)
	}
	sect := f.Sections[ssym.SectionNumber-1]
	data, err := sect.Data()
	if err != nil {
		return nil, err
	}
	return data[ssym.Value:esym.Value], nil
}

// Borrowed from https://golang.org/src/cmd/internal/objfile/pe.go
func findPESymbol(f *pe.File, name string) (*pe.Symbol, error) {
	for _, s := range f.Symbols {
		if s.Name != name {
			continue
		}
		if s.SectionNumber <= 0 {
			return nil, fmt.Errorf("symbol %s: invalid section number %d", name, s.SectionNumber)
		}
		if len(f.Sections) < int(s.SectionNumber) {
			return nil, fmt.Errorf("symbol %s: section number %d is larger than max %d", name, s.SectionNumber, len(f.Sections))
		}
		return s, nil
	}
	return nil, fmt.Errorf("no %s symbol found", name)
}

func openPE(path string) (*dwarf.Data, uint64, Section, *gosym.Table) {
	file, _ := pe.Open(path)
	if file == nil {
		return nil, 0, nil, nil
	}
	dwarf, err := file.DWARF()
	must(err)
	textsect := file.Section(".text")
	textStart, symdat, pclndat, err := pclnPE(file)
	must(err)
	pcln := gosym.NewLineTable(pclndat, uint64(file.Section(".text").Offset))
	tab, err := gosym.NewTable(symdat, pcln)
	must(err)
	return dwarf, textStart, textsect, tab
}

func openMacho(path string) (*dwarf.Data, uint64, Section, *gosym.Table) {
	file, _ := macho.Open(path)
	if file == nil {
		return nil, 0, nil, nil
	}
	dwarf, err := file.DWARF()
	must(err)
	textsect := file.Section("__text")
	
	var (
		symdat  []byte
		pclndat []byte
	)

	if sec := file.Section("__gosymtab"); sec != nil {
		symdat, err = sec.Data()
		must(err)
	}

	if sec := file.Section("__gopclntab"); sec != nil {
		pclndat, err = sec.Data()
		must(err)
	}

	pcln := gosym.NewLineTable(pclndat, textsect.Addr)
	tab, err := gosym.NewTable(symdat, pcln)
	must(err)
	
	return dwarf, textsect.Addr, textsect, tab
}

func openElf(path string) (*dwarf.Data, uint64, Section, *gosym.Table) {
	file, _ := elf.Open(path)
	if file == nil {
		return nil, 0, nil, nil
	}
	dwarf, err := file.DWARF()
	must(err)
	textsect := file.Section(".text")
	
	var (
		symdat  []byte
		pclndat []byte
	)
	
	if sec := file.Section(".gosymtab"); sec != nil {
		symdat, err = sec.Data()
		must(err)
	}

	if sec := file.Section(".gopclntab"); sec != nil {
		pclndat, err = sec.Data()
		must(err)
	}

	pcln := gosym.NewLineTable(pclndat, textsect.Addr)
	tab, err := gosym.NewTable(symdat, pcln)
	must(err)
	
	return dwarf, textsect.Addr, textsect, tab
}

func openExe(exepath string) *Executable {
	for _, fn := range []openFn{openPE, openElf, openMacho} {
		dd, textStart, textSect, goSymbolTable := fn(exepath)
		if dd != nil {
			textData, err := textSect.Data()
			must(err)
			return &Executable{
				Data:      dd,
				TextStart: textStart,
				Text:      textData,
				Gosym: goSymbolTable,
			}
		}
	}
	fmt.Fprintf(os.Stderr, "could not open %s\n", exepath)
	os.Exit(1)
	return nil
}
