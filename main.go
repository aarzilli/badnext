package main

import (
	"bufio"
	"bytes"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
)

func usage() {
	fmt.Fprintf(os.Stderr, `Usage:
	
	badnext succ[essors] <pattern> <executable>

For each function matching pattern lists all acceptable successors of each line.

	badnext check <pattern> <executable> <tag>
	
Checks all functions matching the pattern, prints all mismatches between successors of each line found in the executable and what badnext thinks is acceptable.

Note: only works on amd64 executables.
`)
	os.Exit(1)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

type Pos struct {
	File string
	Line int
}

func digits(n uint64) int {
	if n <= 0 {
		return 1
	}
	return int(math.Floor(math.Log10(float64(n)))) + 1
}

func printSuccessors(succs *Successors, fn *Function) {
	const sourceColSz = 50
	const ellipsis = "…"
	const tab = "    "

	if fn.Decl == nil {
		return
	}

	start, end := succs.ToPos(fn.Decl.Pos()), succs.ToPos(fn.Decl.End())

	fmt.Printf("%s:%d:\n", start.File, start.Line)

	fh, err := os.Open(start.File)
	if err != nil {
		return
	}
	defer fh.Close()
	scanner := bufio.NewScanner(fh)
	i := 0
	prevgroup := uint64(1<<64 - 1)
	for scanner.Scan() {
		i++
		line := scanner.Text()
		if i < start.Line {
			continue
		}
		if i > end.Line {
			break
		}

		line = strings.Replace(line, "\t", tab, -1)

		if len(line) > sourceColSz {
			line = line[:sourceColSz-len(ellipsis)] + ellipsis
		}

		set := succs.S[Pos{start.File, i}]
		group, hasgroup := succs.G[Pos{start.File, i}]
		nextstr := set.String()

		if nextstr == "" && !hasgroup {
			fmt.Printf("%5d %-*s\n", i, sourceColSz, line)
		} else {
			var groupstr string
			if group == prevgroup {
				groupstr = fmt.Sprintf(" %*s %*s ", digits(group>>32), " ", digits(group&groupMask), " ")
			} else {
				groupstr = fmt.Sprintf("[%d.%d]", group>>32, group&groupMask)
			}
			fmt.Printf("%5d %-*s // %s %s\n", i, sourceColSz, line, groupstr, nextstr)
		}
		prevgroup = group
	}
	must(scanner.Err())
	fmt.Println()
}

func (set *PosSet) String() string {
	var buf bytes.Buffer

	if set.Any {
		return "any"
	}

	if len(set.Set) == 0 {
		return ""
	}

	v := make([]int, 0, len(set.Set))
	for k := range set.Set {
		v = append(v, k.Line)
	}
	sort.Ints(v)

	start := v[0]

	flush := func(end int) {
		if end-start > 2 {
			fmt.Fprintf(&buf, "%d-%d ", start, end)
		} else {
			for k := start; k <= end; k++ {
				if k == -1 {
					fmt.Fprintf(&buf, "ret ")
				} else {
					fmt.Fprintf(&buf, "%d ", k)
				}
			}
		}
	}

	for i := 1; i < len(v); i++ {
		if v[i] != v[i-1]+1 {
			flush(v[i-1])
			start = v[i]
		}
	}

	flush(v[len(v)-1])

	return buf.String()
}

var simpleOutput *os.File
var complexOutput *os.File

type OutputKind uint8

const (
	S OutputKind = 1 << iota
	C
)

func printf(k OutputKind, fmtstr string, args ...interface{}) {
	if k&S != 0 {
		fmt.Fprintf(simpleOutput, fmtstr, args...)
	}
	if k&C != 0 {
		fmt.Fprintf(complexOutput, fmtstr, args...)
	}
}

func main() {	
	if len(os.Args) < 4 {
		usage()
	}

	cmd, pattern, exepath := os.Args[1], os.Args[2], os.Args[3]
	exe := openExe(exepath)
	funcs := exe.FunctionsMatching(pattern)
	files := AllFiles(funcs)
	var succs Successors
	for _, file := range files {
		succs.FindSuccessors(file, funcs)
	}

	switch cmd {
	case "succ", "successors":
		for i := range funcs {
			fn := &funcs[i]
			printSuccessors(&succs, fn)
		}
	case "check":
		if len(os.Args) < 5 {
			usage()
		}
		
		tag := os.Args[4]
		var err error
		simpleOutput, err = os.Create(fmt.Sprintf("%s.simple.txt", tag))
		must(err)
		complexOutput, err = os.Create(fmt.Sprintf("%s.full.txt", tag))
		must(err)
		
		penalty := 0
		for i := range funcs {
			penalty += check(&funcs[i], &succs, exe)
		}
		lineCount := 0
		for i := range funcs {
			if funcs[i].Decl == nil {
				continue
			}
			start := succs.ToPos(funcs[i].Decl.Pos())
			end := succs.ToPos(funcs[i].Decl.End())
			lineCount += end.Line - start.Line
		}
		if penalty > 0 {
			printf(S|C, "Average penalty per line: %d/%d = %g\n", penalty, lineCount, float64(penalty)/float64(lineCount))
			os.Exit(1)
		}
	default:
		usage()
	}
}
