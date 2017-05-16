package main

import (
	"debug/dwarf"
	"go/ast"
	"regexp"
	"strings"
	"sort"

	"golang.org/x/arch/x86/x86asm"
)

type Function struct {
	Name        string
	CompileUnit *dwarf.Entry
	Start, End  uint64
	Text        []AsmInstruction
	Decl        ast.Decl
}

type AsmInstruction struct {
	Inst x86asm.Inst
	Pc   uint64
	Pos  Pos
}

func (exe *Executable) FunctionsMatching(pattern string) []Function {
	re := regexp.MustCompile(pattern)

	r := []Function{}

	var cu *dwarf.Entry
	var lnrdr *dwarf.LineReader
	rdr := exe.Data.Reader()
	for {
		entry, err := rdr.Next()
		must(err)
		if entry == nil {
			return r
		}

		switch entry.Tag {
		case dwarf.TagCompileUnit:
			cu = entry
			lnrdr, err = exe.Data.LineReader(cu)
			must(err)
		case dwarf.TagSubprogram:
			name, okname := entry.Val(dwarf.AttrName).(string)
			if !okname || !re.MatchString(name) {
				continue
			}
			if strings.HasSuffix(name, ".init") || strings.Index(name, ".init.") >= 0 {
				continue
			}
			start := entry.Val(dwarf.AttrLowpc).(uint64)
			end := entry.Val(dwarf.AttrHighpc).(uint64)
			r = append(r, Function{
				Name:        name,
				CompileUnit: cu,
				Start:       start,
				End:         end,
				Text:        exe.disassemble(start, end, lnrdr),
			})
		}
	}
}

func (exe *Executable) disassemble(start, end uint64, lnrdr *dwarf.LineReader) []AsmInstruction {
	var lne dwarf.LineEntry
	mem := exe.Text[start-exe.TextStart : end-exe.TextStart]
	r := []AsmInstruction{}
	pc := start
	err := lnrdr.SeekPC(pc, &lne)
	lnevalid := err == nil
	var prevPos Pos
	for len(mem) > 0 {
		inst, err := x86asm.Decode(mem, 64)
		if err == nil {
			var pos Pos
			for lnevalid && lne.Address < pc {
				err := lnrdr.Next(&lne)
				lnevalid = err == nil
			}
			if lnevalid {
				if lne.Address == pc {
					pos.File = lne.File.Name
					pos.Line = lne.Line
				} else {
					pos = prevPos
				}
			}
			prevPos = pos
			
			patchPCRel(pc, &inst)

			r = append(r, AsmInstruction{inst, pc, pos})
			mem = mem[inst.Len:]
			pc += uint64(inst.Len)
		} else {
			r = append(r, AsmInstruction{})
			mem = mem[1:]
			pc++
		}
	}
	return r
}

// converts PC relative arguments to absolute addresses
func patchPCRel(pc uint64, inst *x86asm.Inst) {
	for i := range inst.Args {
		rel, isrel := inst.Args[i].(x86asm.Rel)
		if isrel {
			inst.Args[i] = x86asm.Imm(int64(pc) + int64(rel) + int64(inst.Len))
		}
	}
	return
}

func AllFiles(funcs []Function) []string {
	m := map[string]struct{}{}

	for i := range funcs {
		fn := &funcs[i]
		for i := range fn.Text {
			m[fn.Text[i].Pos.File] = struct{}{}
		}
	}

	r := make([]string, 0, len(m))
	for k := range m {
		r = append(r, k)
	}
	sort.Strings(r)
	return r
}
