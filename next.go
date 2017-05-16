package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"strings"
)

type Successors struct {
	S        map[Pos]PosSet // S[a] is the set of acceptable successors of a
	Sq map[Pos]PosSet // Sm[a] is the set of quasi-acceptable successors of a
	G        map[Pos]uint64 // G[a] is the group identifier of a
	fset     token.FileSet
	curfnend Pos
	curpos   []Pos
	curgroup uint64 // most significant 32bits are a (toplevel) function identifier, least significant 32bits are a line-group identifier
}

const groupMask = uint64(1<<32 - 1)

type PosSet struct {
	Set map[Pos]bool
	Any bool
}

func (s *Successors) addsucc(curpos Pos, vpos ...Pos) {
	set := s.S[curpos]
	if set.Set == nil {
		set.Set = make(map[Pos]bool)
	}
	for _, pos := range vpos {
		if pos != curpos {
			set.Set[pos] = true
		}
	}
	s.S[curpos] = set
}

func (s *Successors) addqsucc(curpos Pos, vpos ...Pos) {
	set := s.Sq[curpos]
	if set.Set == nil {
		set.Set = make(map[Pos]bool)
	}
	for _, pos := range vpos {
		if pos != curpos {
			set.Set[pos] = true
		}
	}
	s.Sq[curpos] = set
}

func acceptedFile(path string) bool {
	return path != "" && strings.Index(path, "<") < 0 && strings.HasSuffix(path, ".go")
}

func (s *Successors) FindSuccessors(path string, funcs []Function) {
	if !acceptedFile(path) {
		return
	}

	n, err := parser.ParseFile(&s.fset, path, nil, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return
	}

	if s.S == nil {
		s.S = make(map[Pos]PosSet)
	}
	if s.G == nil {
		s.G = make(map[Pos]uint64)
	}
	if s.Sq == nil {
		s.Sq = make(map[Pos]PosSet)
	}

	packageName := n.Name.Name

	for _, decl := range n.Decls {
		switch x := decl.(type) {
		case *ast.FuncDecl:
			name := packageName
			if x.Recv != nil {
				var buf bytes.Buffer
				printer.Fprint(&buf, &s.fset, x.Recv.List[0].Type)
				name += ".(" + buf.String() + ")"
			}

			name += "." + x.Name.Name

			found := false
			for i := range funcs {
				if strings.HasSuffix(funcs[i].Name, name) {
					if x.Body != nil {
						funcs[i].Decl = x
					}
					found = true
					break
				}
			}
			if !found || x.Body == nil {
				continue
			}
			s.curpos = []Pos{s.ToPos(x.Pos())}
			s.curfnend = s.ToPos(x.End())
			s.setGroup(s.curpos[0])
			s.findSuccBody(x.Body.Lbrace, x.Body.Rbrace, x.Body.List)
			s.cont(false, Pos{File: "", Line: -1}) // mark end of function
			s.curgroup = ((s.curgroup >> 32) + 1) << 32
		}
	}
}

func (s *Successors) ToPos(pos token.Pos) Pos {
	position := s.fset.Position(pos)
	return Pos{position.Filename, position.Line}
}

func (s *Successors) cont(setPos bool, pos ...Pos) {
	if setPos {
		s.setGroup(pos...)
	}
	for i := range s.curpos {
		s.addsucc(s.curpos[i], pos...)
	}
	s.curpos = pos
}

func (s *Successors) quasiAcceptableCont(pos ...Pos) {
	for i := range s.curpos {
		s.addqsucc(s.curpos[i], pos...)
	}
}

func (s *Successors) alsoCont(setGroup bool, pos ...Pos) {
	if setGroup {
		s.setGroup(pos...)
	}
	for i := range s.curpos {
		s.addsucc(s.curpos[i], pos...)
	}
	s.curpos = append(s.curpos, pos...)
}

func (s *Successors) contAny() {
	for i := range s.curpos {
		s.S[s.curpos[i]] = PosSet{Any: true}
	}
}

func (s *Successors) findSuccBody(lbrace, rbrace token.Pos, list []ast.Stmt) {
	s.curgroup++
	s.alsoCont(true, s.ToPos(lbrace))

	for _, stmt := range list {
		s.findSuccStmt(stmt)
	}

	s.alsoCont(true, s.ToPos(rbrace))
	s.curgroup++
}

func (s *Successors) findSuccStmt(stmt ast.Stmt) {
	switch stmt.(type) {
	case *ast.DeclStmt:
		decl := stmt.(*ast.DeclStmt).Decl.(*ast.GenDecl)
		if decl.Tok == token.VAR {
			for _, spec := range decl.Specs {
				spec := spec.(*ast.ValueSpec)
				s.cont(true, s.allPositions(spec)...)
			}
		}
	case *ast.GoStmt, *ast.SendStmt:
		s.cont(true, s.ToPos(stmt.Pos()), s.ToPos(stmt.End()))
	case *ast.DeferStmt:
		s.cont(true, s.ToPos(stmt.Pos()), s.ToPos(stmt.End()))
		s.alsoCont(false, Pos{ File: "", Line: -1 })
	case *ast.EmptyStmt:
		// Nothing to do
	case *ast.ExprStmt, *ast.AssignStmt, *ast.IncDecStmt:
		s.findSuccExpr(stmt)
	case *ast.ForStmt:
		s.findSuccFor(stmt.(*ast.ForStmt))
	case *ast.RangeStmt:
		s.findSuccRange(stmt.(*ast.RangeStmt))
	case *ast.IfStmt:
		s.findSuccIf(stmt.(*ast.IfStmt))
	case *ast.LabeledStmt:
		x := stmt.(*ast.LabeledStmt)
		s.alsoCont(true, s.ToPos(x.Colon))
		s.findSuccStmt(x.Stmt)
	case *ast.SelectStmt:
		x := stmt.(*ast.SelectStmt)
		s.findSuccSwitch(x.Select, nil, nil, nil, x.Body)
	case *ast.SwitchStmt:
		x := stmt.(*ast.SwitchStmt)
		s.findSuccSwitch(x.Switch, x.Init, x.Tag, nil, x.Body)
	case *ast.TypeSwitchStmt:
		x := stmt.(*ast.TypeSwitchStmt)
		s.findSuccSwitch(x.Switch, x.Init, nil, x.Assign, x.Body)
	case *ast.BranchStmt:
		s.cont(true, s.allPositions(stmt)...)
		s.contAny()
	case *ast.ReturnStmt:
		s.findSuccReturn(stmt)
	default:
		pos := s.ToPos(stmt.Pos())
		fmt.Fprintf(os.Stderr, "%s:%d: unknown statement type %T\n", pos.File, pos.Line, stmt)
	}
}

func (s *Successors) findSuccExpr(x ast.Stmt) {
	positions := s.allPositions(x)
	for _, pos := range positions {
		s.addsucc(pos, positions...)
	}
	s.cont(true, positions...)
}

func (s *Successors) findSuccReturn(x ast.Stmt) {
	positions := s.allPositions(x)
	positions = append(positions, s.curfnend, Pos{ "", -1 })
	s.addsucc(s.curfnend, positions...)
	for _, pos := range positions {
		s.addsucc(pos, positions...)
	}
	s.cont(true, positions...)
}

func (s *Successors) findSuccFor(x *ast.ForStmt) {
	s.curgroup++
	condPositions := s.allPositions(x.Cond)
	condPositions = append(condPositions, s.ToPos(x.For))

	if initPositions := s.allPositions(x.Init); len(initPositions) > 0 {
		s.cont(true, initPositions...)
	}
	s.cont(true, condPositions...)
	s.setGroup(s.allPositions(x.Post)...)
	s.findSuccBody(x.Body.Lbrace, x.Body.Rbrace, x.Body.List)
	if postPositions := s.allPositions(x.Post); len(postPositions) > 0 {
		s.cont(false, postPositions...)
	}
	s.alsoCont(false, condPositions...)
	s.alsoCont(false, s.ToPos(x.Body.Rbrace))
	s.curgroup++
}

func (s *Successors) findSuccRange(x *ast.RangeStmt) {
	s.curgroup++
	s.setGroup(s.allPositions(x.X)...)
	s.cont(true, s.allPositions(x.X)...)
	s.findSuccBody(x.Body.Lbrace, x.Body.Rbrace, x.Body.List)
	s.alsoCont(false, s.ToPos(x.For))
}

func (s *Successors) findSuccIf(ifstmt ast.Stmt) {
	s.curgroup++
	
	headerPositions := []Pos{}
	
	var lastIfCond []Pos
	
	var curposBlockends []Pos
	for ifstmt != nil {
		switch x := ifstmt.(type) {
		case *ast.IfStmt:
			condPositions := s.allPositions(x.Cond)
			lastIfCond = condPositions
			
			headerPositions = append(headerPositions, condPositions...)

			if initPositions := s.allPositions(x.Init); len(initPositions) > 0 {
				s.cont(true, initPositions...)
			}
			s.cont(true, condPositions...)
			curposLastcond := s.curposSave()
			s.findSuccBody(x.Body.Lbrace, x.Body.Rbrace, x.Body.List)
			curposBlockends = append(curposBlockends, s.curpos...)
			s.quasiAcceptableCont(curposLastcond...)
			s.curpos = curposLastcond
			ifstmt = x.Else

		case *ast.BlockStmt:
			s.findSuccBody(x.Lbrace, x.Rbrace, x.List)
			curposBlockends = append(curposBlockends, s.curpos...)
			s.quasiAcceptableCont(lastIfCond...)
			s.curpos = []Pos{}
			ifstmt = nil
		}
	}
		
	s.curpos = append(s.curpos, curposBlockends...)
	s.curpos = append(s.curpos, headerPositions...)
}

func (s *Successors) findSuccSwitch(key token.Pos, init ast.Stmt, tag ast.Expr, assign ast.Stmt, body *ast.BlockStmt) {
	s.curgroup++
	if initPositions := s.allPositions(init); len(initPositions) > 0 {
		s.cont(true, initPositions...)
	}
	tagPositions := s.allPositions(tag)
	tagPositions = append(tagPositions, s.ToPos(key))
	s.cont(true, tagPositions...)

	groupHeader := s.curgroup
	curposHeader := s.curposSave()
	clausePositions := []Pos{}
	curposBlockends := []Pos{}

	for _, stmt := range body.List {
		var clauseHeader []ast.Node
		switch stmt := stmt.(type) {
		case *ast.CaseClause:
			clauseHeader = make([]ast.Node, len(stmt.List))
			for i := range stmt.List {
				clauseHeader[i] = stmt.List[i]
			}
		case *ast.CommClause:
			clauseHeader = []ast.Node{stmt.Comm}
		}

		s.curpos = []Pos{}
		for _, x := range clauseHeader {
			vpos := s.allPositions(x)
			s.setGroup(vpos...)
			s.curpos = append(s.curpos, vpos...)
		}
		clausePos := s.ToPos(stmt.Pos())
		s.setGroup(clausePos)
		s.curpos = append(s.curpos, clausePos)

		clausePositions = append(clausePositions, s.curpos...)

		if assign != nil {
			s.cont(false, s.allPositions(assign)...)
		}

		switch stmt := stmt.(type) {
		case *ast.CaseClause:
			s.findSuccBody(stmt.Colon, body.Rbrace, stmt.Body)
		case *ast.CommClause:
			s.findSuccBody(stmt.Colon, body.Rbrace, stmt.Body)
		}
		curposBlockends = append(curposBlockends, s.curpos...)
		s.quasiAcceptableCont(tagPositions...)
	}

	for _, pos := range clausePositions {
		s.G[pos] = groupHeader
		s.addsucc(pos, clausePositions...)
		s.addqsucc(pos, tagPositions...)
	}

	s.curpos = curposHeader
	s.alsoCont(false, clausePositions...)
	s.alsoCont(false, s.ToPos(body.Rbrace))
	s.curpos = append(s.curpos, curposBlockends...)
}

func (s *Successors) curposSave() []Pos {
	r := make([]Pos, len(s.curpos))
	copy(r, s.curpos)
	return r
}

func (s *Successors) allPositions(x ast.Node) []Pos {
	if x == nil {
		return nil
	}
	if !x.Pos().IsValid() || !x.End().IsValid() {
		return nil
	}
	r := []Pos{}
	var last Pos
	for p := x.Pos(); p < x.End(); p++ {
		cur := s.ToPos(p)
		if cur != last {
			if last.File != "" && cur.File != last.File {
				return nil
			}
			r = append(r, cur)
			last = cur
		}
	}
	return r
}

func (s *Successors) setGroup(vpos ...Pos) {
	for _, pos := range vpos {
		if _, hasgroup := s.G[pos]; !hasgroup {
			s.G[pos] = s.curgroup
		}
	}
}
