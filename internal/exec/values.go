package exec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/kylemclaren/jevql/internal/psqlout"
)

// rightAligned reports whether psql would right-align this type.
func rightAligned(oid uint32) bool {
	switch oid {
	case pgtype.Int2OID, pgtype.Int4OID, pgtype.Int8OID, pgtype.Float4OID, pgtype.Float8OID, pgtype.NumericOID, pgtype.OIDOID, 790 /* money */ :
		return true
	}
	return false
}

// cellFromRaw builds a display cell from a text-format wire value.
func cellFromRaw(fd pgconn.FieldDescription, raw []byte, val any) psqlout.Cell {
	if raw == nil {
		return psqlout.Cell{Null: true}
	}
	return psqlout.Cell{Text: string(raw), Value: val, Right: rightAligned(fd.DataTypeOID)}
}

func floatCell(f float64) psqlout.Cell {
	return psqlout.Cell{Text: strconv.FormatFloat(f, 'g', -1, 64), Value: f, Right: true}
}

func intCell(i int64) psqlout.Cell {
	return psqlout.Cell{Text: strconv.FormatInt(i, 10), Value: i, Right: true}
}

func boolCell(b bool) psqlout.Cell {
	t := "f"
	if b {
		t = "t"
	}
	return psqlout.Cell{Text: t, Value: b}
}

func textCell(s string) psqlout.Cell { return psqlout.Cell{Text: s, Value: s} }

func jsonCell(raw json.RawMessage) psqlout.Cell {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return psqlout.Cell{Text: string(raw), Value: raw}
	}
	return psqlout.Cell{Text: buf.String(), Value: raw}
}

// toFloat converts numeric database values.
func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int8:
		return float64(x), true
	case int16:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint8:
		return float64(x), true
	case uint16:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	case float32:
		return float64(x), true
	case float64:
		return x, true
	case pgtype.Numeric:
		if !x.Valid {
			return 0, false
		}
		f, err := x.Float64Value()
		if err != nil || !f.Valid {
			return 0, false
		}
		return f.Float64, true
	case *big.Int:
		f, _ := new(big.Float).SetInt(x).Float64()
		return f, true
	case string:
		f, err := strconv.ParseFloat(x, 64)
		return f, err == nil
	}
	return 0, false
}

func toInt(v any) (int64, bool) {
	switch x := v.(type) {
	case int:
		return int64(x), true
	case int8:
		return int64(x), true
	case int16:
		return int64(x), true
	case int32:
		return int64(x), true
	case int64:
		return x, true
	case uint8:
		return int64(x), true
	case uint16:
		return int64(x), true
	case uint32:
		return int64(x), true
	}
	return 0, false
}

// compare orders two non-nil values the way Postgres would for common types.
func compare(a, b any) int {
	if fa, ok := toFloat(a); ok {
		if fb, ok := toFloat(b); ok {
			switch {
			case fa < fb:
				return -1
			case fa > fb:
				return 1
			}
			return 0
		}
	}
	switch x := a.(type) {
	case bool:
		if y, ok := b.(bool); ok {
			switch {
			case !x && y:
				return -1
			case x && !y:
				return 1
			}
			return 0
		}
	case time.Time:
		if y, ok := b.(time.Time); ok {
			switch {
			case x.Before(y):
				return -1
			case x.After(y):
				return 1
			}
			return 0
		}
	case pgtype.Date:
		if y, ok := b.(pgtype.Date); ok {
			return compare(x.Time, y.Time)
		}
	case string:
		if y, ok := b.(string); ok {
			return strings.Compare(x, y)
		}
	}
	return strings.Compare(fmt.Sprint(a), fmt.Sprint(b))
}
