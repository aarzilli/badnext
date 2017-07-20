package main

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/arch/x86/x86asm"
)

const (
	OutOfOrderPenalty    = 1   // moves to a different line but in the same group of lines
	OutOfGroupPenalty    = 10  // moves to a different line, not in the group of lines we expected
	OutOfFunctionPenalty = 100 // moves to a different line, in a different function?!
)

var verboseCheck = false

func check(fn *Function, succs *Successors, exe *Executable) int {
	if fn.Decl == nil {
		return 0
	}
	if verboseCheck {
		fmt.Fprintf(os.Stderr, "FUNCTION %s\n", fn.Name)
	}

	var curpos Pos
	var penalty int

	t := func(start, end Pos, pc uint64) {
		penalty += succs.checkTransition(start, end, pc)
		curpos = end
	}

	symlookup := func(pc uint64) (string, uint64) {
		dest := exe.Gosym.PCToFunc(pc)
		if dest == nil {
			return "", 0
		}
		if dest.Entry == fn.Text[0].Pc {
			return "", 0
		}
		return dest.Name, dest.Entry
	}

	for _, inst := range fn.Text {
		if verboseCheck {
			fmt.Fprintf(os.Stderr, "%s:%d\t%#x\t%s\n", filepath.Base(inst.Pos.File), inst.Pos.Line, inst.Pc, x86asm.GoSyntax(inst.Inst, inst.Pc, symlookup))
		}
		if curpos.File == "" && curpos.Line == 0 {
			curpos = inst.Pos
		}

		/*
			if inst.Inst.Op == x86asm.CALL {
				// Any call to a function starting with runtime.panic does not return and
				// can appear anywhere, so it shouldn't be considered a transition.
				// TODO: actually check the destination of the call
				curpos = Pos{"", -1}
				continue
			}*/

		if inst.Inst.Op == x86asm.UD1 || inst.Inst.Op == x86asm.UD2 {
			// undefined instruction, assume we can never get here
			curpos = Pos{"", -1}
			continue
		}

		if curpos != inst.Pos {
			t(curpos, inst.Pos, inst.Pc)
		}

		if jmpdest, unconditional := isJump(fn, inst); jmpdest >= 0 {
			if fn.Text[jmpdest].Pos != curpos {
				penalty += succs.checkTransition(curpos, fn.Text[jmpdest].Pos, inst.Pc)
			}
			if unconditional {
				curpos = Pos{}
			}
		}

		if isRet(fn, inst) {
			t(curpos, Pos{"", -1}, inst.Pc)
		}
	}

	if (curpos.File != "" || curpos.Line != 0) && len(fn.Text) > 0 {
		t(curpos, Pos{"", -1}, fn.Text[len(fn.Text)-1].Pc)
	}

	if verboseCheck {
		fmt.Fprintf(os.Stderr, "\n")
	}

	return penalty
}

func isJump(fn *Function, inst AsmInstruction) (destIdx int, unconditional bool) {
	switch inst.Inst.Op {
	case x86asm.JA, x86asm.JAE, x86asm.JB, x86asm.JBE, x86asm.JCXZ, x86asm.JE, x86asm.JECXZ, x86asm.JG, x86asm.JGE, x86asm.JL, x86asm.JLE, x86asm.JNE, x86asm.JNO, x86asm.JNP, x86asm.JNS, x86asm.JO, x86asm.JP, x86asm.JRCXZ, x86asm.JS, x86asm.LOOPE, x86asm.LOOPNE, x86asm.LOOP:
		//ok
	case x86asm.JMP, x86asm.LJMP:
		unconditional = true
	default:
		return -1, false
	}

	if len(inst.Inst.Args) < 1 {
		return -1, false
	}

	imm, isimm := inst.Inst.Args[0].(x86asm.Imm)
	if !isimm {
		return -1, false
	}

	for i := range fn.Text {
		if fn.Text[i].Pc == uint64(imm) {
			return i, unconditional
		}
	}

	fmt.Fprintf(os.Stderr, "could not find destination of jump at %#x (destination pc %#x)\n", inst.Pc, imm)
	return -1, false
}

func isRet(fn *Function, inst AsmInstruction) bool {
	switch inst.Inst.Op {
	case x86asm.RET, x86asm.LRET:
		return true
	default:
		return false
	}
}

func (s *Successors) checkTransition(start, end Pos, pc uint64) int {
	if !acceptedFile(start.File) {
		return 0
	}
	if a := s.S[start]; a.Contains(end) || a.Any {
		return 0
	}

	endgroup := s.G[end]

	// do not report exit from if/switch
	if a := s.Sq[start]; a.Contains(end) {
		return 0
	}

	fmt.Fprintf(os.Stderr, "%s:%d: (%#x) continues to %s:%d, expected:\n", start.File, start.Line, pc, end.File, end.Line)

	penalty := OutOfFunctionPenalty
	if end.File == "" && end.Line == -1 {
		penalty = OutOfGroupPenalty
	}

	for k := range s.S[start].Set {
		fmt.Fprintf(os.Stderr, "\t%s:%d\n", k.File, k.Line)
		p := 0
		if s.G[k] == endgroup {
			p = OutOfOrderPenalty
		} else if s.G[k]>>32 == endgroup>>32 {
			p = OutOfGroupPenalty
		} else {
			p = OutOfFunctionPenalty
		}
		if p < penalty {
			penalty = p
		}
	}

	if a := s.Sq[start]; a.Contains(end) {
		fmt.Fprintf(os.Stderr, "\t(exit from if or switch)\n")
		if penalty > OutOfOrderPenalty {
			penalty = OutOfOrderPenalty
		}
	}

	fmt.Fprintf(os.Stderr, "\tpenalty: +%d\n", penalty)
	return penalty
}

func (set *PosSet) Contains(p Pos) bool {
	if set.Set == nil {
		return false
	}
	return set.Set[p]
}
