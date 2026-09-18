// Package parse turns SQL text into an analysed statement using pg_query
// (libpg_query). It finds jev_* calls, validates where they appear and
// extracts everything the rewriter needs. No regexes touch the SQL.
package parse

import (
	"strings"

	pg_query "github.com/pganalyze/pg_query_go/v6"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Walk visits every protobuf message reachable from m in document order.
// visit returns false to stop descending into that subtree.
func Walk(m proto.Message, visit func(proto.Message) bool) {
	if m == nil {
		return
	}
	if !m.ProtoReflect().IsValid() {
		return
	}
	if !visit(m) {
		return
	}
	r := m.ProtoReflect()
	r.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if fd.Kind() != protoreflect.MessageKind || fd.IsMap() {
			return true
		}
		if fd.IsList() {
			l := v.List()
			for i := 0; i < l.Len(); i++ {
				Walk(l.Get(i).Message().Interface(), visit)
			}
			return true
		}
		Walk(v.Message().Interface(), visit)
		return true
	})
}

// FuncName returns the lower-cased unqualified function name of a FuncCall.
func FuncName(fc *pg_query.FuncCall) string {
	if fc == nil || len(fc.Funcname) == 0 {
		return ""
	}
	last := fc.Funcname[len(fc.Funcname)-1].GetString_()
	if last == nil {
		return ""
	}
	return strings.ToLower(last.Sval)
}

// IsJevFunc reports whether name is one of the jev_* SQL functions.
func IsJevFunc(name string) bool {
	switch name {
	case "jev", "jev_prob", "jev_choice", "jev_score", "jev_score_norm", "jev_confidence", "jev_eval":
		return true
	}
	return false
}

// ContainsJev reports whether any jev_* FuncCall appears under n.
func ContainsJev(n proto.Message) bool {
	found := false
	Walk(n, func(m proto.Message) bool {
		if found {
			return false
		}
		if fc, ok := m.(*pg_query.FuncCall); ok && IsJevFunc(FuncName(fc)) {
			found = true
			return false
		}
		return true
	})
	return found
}

// JevCalls returns every jev_* FuncCall under n, in document order.
func JevCalls(n proto.Message) []*pg_query.FuncCall {
	var out []*pg_query.FuncCall
	Walk(n, func(m proto.Message) bool {
		if fc, ok := m.(*pg_query.FuncCall); ok && IsJevFunc(FuncName(fc)) {
			out = append(out, fc)
		}
		return true
	})
	return out
}

// DeparseExpr renders a single expression node back to SQL text.
func DeparseExpr(n *pg_query.Node) string {
	if n == nil {
		return ""
	}
	sel := &pg_query.SelectStmt{
		TargetList:  []*pg_query.Node{pg_query.MakeResTargetNodeWithVal(proto.Clone(n).(*pg_query.Node), -1)},
		LimitOption: pg_query.LimitOption_LIMIT_OPTION_DEFAULT,
		Op:          pg_query.SetOperation_SETOP_NONE,
	}
	tree := &pg_query.ParseResult{Stmts: []*pg_query.RawStmt{{Stmt: &pg_query.Node{Node: &pg_query.Node_SelectStmt{SelectStmt: sel}}}}}
	s, err := pg_query.Deparse(tree)
	if err != nil {
		return "<expr>"
	}
	return strings.TrimPrefix(s, "SELECT ")
}

// DeparseStmt renders a full statement node to SQL text.
func DeparseStmt(stmt *pg_query.Node) (string, error) {
	tree := &pg_query.ParseResult{Stmts: []*pg_query.RawStmt{{Stmt: stmt}}}
	return pg_query.Deparse(tree)
}
