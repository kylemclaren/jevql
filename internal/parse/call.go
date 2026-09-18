package parse

import (
	"fmt"
	"strconv"
	"strings"

	pg_query "github.com/pganalyze/pg_query_go/v6"
)

// Kind is the TypeSafe question type a call maps to.
type Kind string

const (
	KindNoul   Kind = "noul"
	KindChoice Kind = "choice"
	KindScore  Kind = "score"
)

// Source describes where the row object for a call comes from: either a
// FROM alias (all of its columns) or an explicit column list.
type Source struct {
	Alias   string           // set when IsAlias
	IsAlias bool             //
	Columns []*pg_query.Node // column-list form: each element is an expression
	Names   []string         // output names for Columns
}

// Key is a stable identity for the source so identical sources share a
// row object.
func (s Source) Key() string {
	if s.IsAlias {
		return "alias:" + s.Alias
	}
	parts := make([]string, len(s.Columns))
	for i, c := range s.Columns {
		parts[i] = DeparseExpr(c)
	}
	return "cols:" + strings.Join(parts, ",")
}

// String renders the source as the user wrote it.
func (s Source) String() string {
	if s.IsAlias {
		return s.Alias
	}
	parts := make([]string, len(s.Columns))
	for i, c := range s.Columns {
		parts[i] = DeparseExpr(c)
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// Call is one parsed jev_* invocation.
type Call struct {
	Fn        string   // sql function name: jev, jev_prob, ...
	Kind      Kind     // question kind sent to TypeSafe
	Source    Source   //
	Question  string   //
	Options   []string // choice/score levels
	Threshold *float64 // jev(alias, q, 0.7)
	Text      string   // deparsed original expression
	Location  int32    //
}

// QuestionKey identifies the (kind, question, options) tuple shared by
// several calls so they are judged once per row.
func (c *Call) QuestionKey() string {
	return string(c.Kind) + "\x00" + c.Question + "\x00" + strings.Join(c.Options, "\x01")
}

// ParseCall interprets a jev_* FuncCall. It returns an error with the
// original expression text when the arguments do not fit the SQL surface.
func ParseCall(fc *pg_query.FuncCall) (*Call, error) {
	name := FuncName(fc)
	text := DeparseExpr(&pg_query.Node{Node: &pg_query.Node_FuncCall{FuncCall: fc}})
	c := &Call{Fn: name, Text: text, Location: fc.Location}
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%s: %s", text, fmt.Sprintf(format, a...))
	}
	if fc.AggStar || fc.AggDistinct || fc.Over != nil || fc.AggFilter != nil {
		if fc.Over != nil {
			return nil, bad("window functions over jev_* are not supported")
		}
		return nil, bad("unexpected aggregate syntax on jev_* call")
	}
	if len(fc.Args) < 2 {
		return nil, bad("needs at least (alias_or_columns, 'question')")
	}
	src, err := parseSource(fc.Args[0])
	if err != nil {
		return nil, bad("%v", err)
	}
	c.Source = src
	q, ok := constString(fc.Args[1])
	if !ok {
		return nil, bad("second argument must be a string literal")
	}
	c.Question = q
	rest := fc.Args[2:]
	switch name {
	case "jev", "jev_prob":
		c.Kind = KindNoul
		if name == "jev" && len(rest) == 1 {
			f, ok := constFloat(rest[0])
			if !ok {
				return nil, bad("threshold must be a numeric literal")
			}
			c.Threshold = &f
		} else if len(rest) != 0 {
			return nil, bad("too many arguments")
		}
	case "jev_choice":
		c.Kind = KindChoice
		if len(rest) != 1 {
			return nil, bad("needs (alias, 'question', ARRAY['a','b'])")
		}
		c.Options, ok = constStringArray(rest[0])
		if !ok || len(c.Options) == 0 {
			return nil, bad("options must be a non-empty ARRAY of string literals")
		}
	case "jev_score", "jev_score_norm":
		c.Kind = KindScore
		if len(rest) != 1 {
			return nil, bad("needs (alias, 'question', ARRAY['lo','mid','hi'])")
		}
		c.Options, ok = constStringArray(rest[0])
		if !ok || len(c.Options) < 2 {
			return nil, bad("levels must be an ARRAY of at least two string literals")
		}
	case "jev_confidence", "jev_eval":
		// (alias, q) -> noul; (alias, q, 'choice'|'score'|'noul', ARRAY[...])
		c.Kind = KindNoul
		if len(rest) >= 1 {
			k, ok := constString(rest[0])
			if !ok {
				return nil, bad("third argument must be 'noul', 'choice' or 'score'")
			}
			switch strings.ToLower(k) {
			case "noul":
				c.Kind = KindNoul
			case "choice":
				c.Kind = KindChoice
			case "score":
				c.Kind = KindScore
			default:
				return nil, bad("unknown kind %q (want noul, choice or score)", k)
			}
		}
		if len(rest) >= 2 {
			c.Options, ok = constStringArray(rest[1])
			if !ok {
				return nil, bad("options must be an ARRAY of string literals")
			}
		}
		if len(rest) > 2 {
			return nil, bad("too many arguments")
		}
		if c.Kind != KindNoul && len(c.Options) == 0 {
			return nil, bad("%s questions need an ARRAY of options", c.Kind)
		}
	default:
		return nil, bad("unknown jev function")
	}
	return c, nil
}

func parseSource(n *pg_query.Node) (Source, error) {
	switch v := n.Node.(type) {
	case *pg_query.Node_ColumnRef:
		fields := v.ColumnRef.Fields
		if len(fields) == 1 {
			if s := fields[0].GetString_(); s != nil {
				return Source{IsAlias: true, Alias: s.Sval}, nil
			}
		}
		return Source{}, fmt.Errorf("first argument must be a FROM alias or a column list like (name, bio), got %s", DeparseExpr(n))
	case *pg_query.Node_RowExpr:
		src := Source{}
		if len(v.RowExpr.Args) == 0 {
			return src, fmt.Errorf("column list is empty")
		}
		for i, a := range v.RowExpr.Args {
			src.Columns = append(src.Columns, a)
			src.Names = append(src.Names, exprName(a, i))
		}
		return src, nil
	}
	return Source{}, fmt.Errorf("first argument must be a FROM alias or a column list like (name, bio), got %s", DeparseExpr(n))
}

// exprName picks a JSON key for a column-list element.
func exprName(n *pg_query.Node, i int) string {
	if cr := n.GetColumnRef(); cr != nil && len(cr.Fields) > 0 {
		if s := cr.Fields[len(cr.Fields)-1].GetString_(); s != nil {
			return s.Sval
		}
	}
	if fc := n.GetFuncCall(); fc != nil {
		return FuncName(fc)
	}
	return "col" + strconv.Itoa(i+1)
}

func constString(n *pg_query.Node) (string, bool) {
	ac := n.GetAConst()
	if ac == nil || ac.GetSval() == nil {
		return "", false
	}
	return ac.GetSval().Sval, true
}

func constFloat(n *pg_query.Node) (float64, bool) {
	ac := n.GetAConst()
	if ac == nil {
		return 0, false
	}
	if iv := ac.GetIval(); iv != nil {
		return float64(iv.Ival), true
	}
	if fv := ac.GetFval(); fv != nil {
		f, err := strconv.ParseFloat(fv.Fval, 64)
		return f, err == nil
	}
	return 0, false
}

func constStringArray(n *pg_query.Node) ([]string, bool) {
	arr := n.GetAArrayExpr()
	if arr == nil {
		// allow ARRAY[...]::text[]
		if tc := n.GetTypeCast(); tc != nil {
			return constStringArray(tc.Arg)
		}
		return nil, false
	}
	out := make([]string, 0, len(arr.Elements))
	for _, e := range arr.Elements {
		s, ok := constString(e)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}
