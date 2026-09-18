// Package canon converts database values into canonical JSON. The bytes it
// produces feed the cache key, so the rules here must never change:
// objects have sorted keys, timestamps are RFC 3339 with nanoseconds in UTC,
// NULL fields are included as null, and there is no insignificant whitespace.
package canon

import (
	"bytes"
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/netip"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// Value converts a pgx-decoded value into something json.Marshal renders
// deterministically.
func Value(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case bool, string, float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return x
	case json.Number:
		return x
	case time.Time:
		return x.UTC().Format(time.RFC3339Nano)
	case time.Duration:
		return x.String()
	case []byte:
		return base64.StdEncoding.EncodeToString(x)
	case [16]byte:
		return fmt.Sprintf("%x-%x-%x-%x-%x", x[0:4], x[4:6], x[6:8], x[8:10], x[10:16])
	case netip.Prefix:
		return x.String()
	case netip.Addr:
		return x.String()
	case pgtype.Numeric:
		if !x.Valid {
			return nil
		}
		if x.NaN {
			return "NaN"
		}
		if x.InfinityModifier != pgtype.Finite {
			return x.InfinityModifier.String()
		}
		// Render exactly as Postgres would (no float rounding).
		b, err := x.MarshalJSON()
		if err == nil {
			return json.RawMessage(b)
		}
		return new(big.Float).SetInt(x.Int).Text('f', -1)
	case pgtype.Date:
		if !x.Valid {
			return nil
		}
		return x.Time.Format("2006-01-02")
	case pgtype.Interval:
		if !x.Valid {
			return nil
		}
		b, _ := x.Value()
		return b
	case map[string]any:
		return Object(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = Value(e)
		}
		return out
	case []string:
		return x
	case []int64:
		return x
	case []float64:
		return x
	case []bool:
		return x
	case driver.Valuer:
		dv, err := x.Value()
		if err != nil {
			return fmt.Sprint(v)
		}
		if dv == nil {
			return nil
		}
		if _, ok := dv.(driver.Valuer); ok {
			return fmt.Sprint(dv)
		}
		return Value(dv)
	case fmt.Stringer:
		return x.String()
	}
	// Slices of other types: try JSON round trip.
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return json.RawMessage(b)
}

// Object is a JSON object that marshals with sorted keys.
type Object map[string]any

// MarshalJSON renders keys in sorted order.
func (o Object) MarshalJSON() ([]byte, error) {
	keys := make([]string, 0, len(o))
	for k := range o {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		buf.Write(kb)
		buf.WriteByte(':')
		vb, err := json.Marshal(Value(o[k]))
		if err != nil {
			return nil, err
		}
		buf.Write(vb)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// Marshal renders an object canonically.
func Marshal(o map[string]any) ([]byte, error) {
	return Object(o).MarshalJSON()
}
