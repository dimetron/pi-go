package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestServeBindsReadyBeforeServing pins the ordering a readiness probe depends
// on: the readiness listener is claimed before the main server starts accepting,
// so /health answering implies /readyz answers.
//
// It asserts the source order rather than racing the scheduler, deliberately.
// The window between "main server accepting" and "ready listener bound" was real
// but too narrow to hit on an idle machine — 0 of 200 iterations — so it only
// ever appeared on a loaded CI runner, as an occasional `connection refused` in
// TestServeReadyz. A timing test here would pass whether or not the ordering was
// correct, which is worse than no test. Reading the AST is deterministic and
// fails the moment someone moves the bind back below the server start.
func TestServeBindsReadyBeforeServing(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "server.go", nil, 0)
	if err != nil {
		t.Fatalf("parse server.go: %v", err)
	}

	var serveFn *ast.FuncDecl
	for _, decl := range file.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok && fd.Name.Name == "Serve" {
			serveFn = fd
			break
		}
	}
	if serveFn == nil {
		t.Fatal("Serve not found in server.go")
	}

	var readyBind, mainServe token.Pos
	ast.Inspect(serveFn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || len(call.Args) < 2 {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "net" {
			return true
		}
		addrArg, ok := call.Args[1].(*ast.Ident)
		if !ok {
			return true
		}
		// Serve claims the readiness port with net.Listen("tcp", readyAddr).
		if sel.Sel.Name == "Listen" && addrArg.Name == "readyAddr" && !readyBind.IsValid() {
			readyBind = call.Pos()
		}
		return true
	})

	ast.Inspect(serveFn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		// srv.Serve(ln) is what starts accepting on the main listener.
		if sel.Sel.Name == "Serve" && !mainServe.IsValid() {
			mainServe = call.Pos()
		}
		return true
	})

	if !readyBind.IsValid() {
		t.Fatal(`no net.Listen("tcp", readyAddr) found in Serve`)
	}
	if !mainServe.IsValid() {
		t.Fatal("no server Serve(...) call found in Serve")
	}
	if readyBind > mainServe {
		t.Errorf("readiness listener is bound at line %d, after the main server starts at line %d; "+
			"a probe can see /health answer while /readyz is still refused",
			fset.Position(readyBind).Line, fset.Position(mainServe).Line)
	}
}
