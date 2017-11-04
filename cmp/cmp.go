package main

import (
	"os"
	"io/ioutil"
	"fmt"
	"bufio"
	"strings"
	"sort"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

type Run struct {
	Descr string
	Errs map[string]*Err
}

type Err struct {
	PC string
	Descr string
}

func read(name string) Run {
	buf, err := ioutil.ReadFile(fmt.Sprintf("out/%s.description.txt", name))
	must(err)
	descr := string(buf)
	
	fh, err := os.Open(fmt.Sprintf("out/%s.simple.txt", name))
	must(err)
	s := bufio.NewScanner(fh)
	
	errs := map[string]*Err{}
	
	for s.Scan() {
		if len(s.Text()) == 0 {
			continue
		}
		fields := strings.SplitN(s.Text(), ":", 4)
		if len(fields) != 4 {
			continue
		}
		errs[fields[0]+":"+fields[1]] = &Err{ PC: fields[2], Descr: fields[3] }
	}
	
	return Run{ descr, errs }
}

func main() {
	oldName := os.Args[1]
	newName := os.Args[2]
	
	old := read(oldName)
	new := read(newName)
	
	improved := []string{}
	regressed := []string{}
	
	for path, err := range old.Errs {
		if new.Errs[path] == nil {
			improved = append(improved, fmt.Sprintf("%s (%s) %s", path, err.PC, err.Descr))
		}
	}
	
	for path, err := range new.Errs {
		if old.Errs[path] == nil {
			regressed = append(regressed, fmt.Sprintf("%s (%s)%s", path, err.PC, err.Descr))
		}
	}
	
	sort.Strings(improved)
	sort.Strings(regressed)
	
	fmt.Printf("Comparing: %s\n       To: %s\n\n", oldName, newName)
	fmt.Printf("Improved:\n")
	for _, err := range improved {
		fmt.Printf("%s\n", err)
	}
	fmt.Printf("\n\nRegressed:\n")
	for _, err := range regressed {
		fmt.Printf("%s\n", err)
	}
}
