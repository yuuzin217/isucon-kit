package pgstat

import (
	"strings"
	"testing"
)

const fixture = `{"version":1,"database":"app","captured_at":"2026-10-31T14:00:00+09:00","stats_reset":"2026-10-31T13:00:00+09:00","hidden_queries":0,"queries":[{"query":"SELECT id FROM users WHERE secret = 'private-value' AND id = $1 /* hidden-comment */","calls":4,"total_exec_time":2500,"max_exec_time":1000}]}`

func TestSnapshotUnitsAndPrivacy(t *testing.T) {
	s, err := Decode([]byte(fixture))
	if err != nil {
		t.Fatal(err)
	}
	row := s.Stats()[0]
	if row.Count != 4 || row.TotalSeconds != 2.5 || row.AvgSeconds != 0.625 || row.MaxSeconds != 1 {
		t.Fatalf("wrong units: %+v", row)
	}
	if strings.Contains(row.Fingerprint, "private-value") || strings.Contains(row.Fingerprint, "hidden-comment") || !strings.Contains(row.Fingerprint, "$1") {
		t.Fatal(row.Fingerprint)
	}
	if !strings.Contains(s.Scope("db1"), "2026-10-31T04:00:00Z") {
		t.Fatal(s.Scope("db1"))
	}
}

func TestSnapshotRejectsInvalidAndHidden(t *testing.T) {
	for _, body := range []string{"", `{}`, fixture + `{}`, strings.Replace(fixture, `"hidden_queries":0`, `"hidden_queries":1`, 1), strings.Replace(fixture, `"calls":4`, `"calls":-1`, 1), strings.Replace(fixture, `2500`, `-1`, 1), strings.Replace(fixture, `1000`, `1e999`, 1), strings.Replace(fixture, `13:00:00`, `15:00:00`, 1)} {
		if _, err := Decode([]byte(body)); err == nil {
			t.Fatalf("accepted invalid snapshot: %s", body)
		}
	}
	empty := fixture[:strings.Index(fixture, `"queries":`)] + `"queries":[]}`
	s, err := Decode([]byte(empty))
	if err != nil || len(s.Stats()) != 0 {
		t.Fatalf("empty snapshot: %+v %v", s, err)
	}
}

func TestFingerprintPostgresLiterals(t *testing.T) {
	cases := []struct{ sql, want string }{
		{`SELECT $1, 'private-value', 123, 1e-3, col2`, `SELECT $1, ?, ?, ?, col2`},
		{`SELECT E'private\'value', 'other-value'`, `SELECT E?, ?`},
		{`DO $tag$private-value$tag$; SELECT $$other-value$$`, `DO ?; SELECT ?`},
		{"SELECT /* private /* nested */ value */ id -- secret\nFROM users", "SELECT   id  FROM users"},
		{`SELECT "two  spaces", 'private''value'`, `SELECT "two  spaces", ?`},
		{`SELECT 'private\' AS x, 'other-value'`, `SELECT ?`},
		{`SELECT 'unterminated-private`, `SELECT ?`},
	}
	for _, c := range cases {
		got := Fingerprint(c.sql)
		if got != c.want {
			t.Errorf("got %q want %q", got, c.want)
		}
		if twice := Fingerprint(got); twice != got {
			t.Errorf("not idempotent: %q vs %q", got, twice)
		}
	}
}
