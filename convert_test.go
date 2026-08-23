package main

import (
	"math"
	"testing"
)

// Reference vectors generated from the soundcraft-ui TypeScript implementation
// (value-converters.ts) with node; see the milestone-4 report. Gate G4 requires
// agreement within 0.05 dB.

func TestFaderValueToDB(t *testing.T) {
	cases := []struct {
		pos float64
		db  float64
	}{
		{0.0, math.Inf(-1)},
		{0.055, -60.7},
		{0.1, -52.9},
		{0.2, -38.2},
		{0.3, -26.8},
		{0.4, -18.2},
		{0.5, -11.6},
		{0.6, -6.6},
		{0.7, -2.4},
		{0.8, 1.3},
		{0.9, 5.3},
		{1.0, 10},
	}
	for _, c := range cases {
		got := faderValueToDB(c.pos)
		if math.IsInf(c.db, -1) {
			if !math.IsInf(got, -1) {
				t.Errorf("faderValueToDB(%v) = %v, want -Inf", c.pos, got)
			}
			continue
		}
		if math.Abs(got-c.db) > 0.05 {
			t.Errorf("faderValueToDB(%v) = %v, want %v (±0.05 dB)", c.pos, got, c.db)
		}
	}
}

func TestDBToFaderValue(t *testing.T) {
	cases := []struct {
		db  float64
		pos float64
	}{
		{-80, 0.01038584404},
		{-40, 0.18623016528},
		{-20, 0.37650583661},
		{-10, 0.52941176471},
		{0, 0.76470588235},
		{10, 1},
	}
	for _, c := range cases {
		got := dbToFaderValue(c.db)
		// Position agreement checked in dB space so the 0.05 dB gate applies at
		// the value the user actually reads.
		if !dbClose(got, c.pos, 0.05) {
			t.Errorf("dbToFaderValue(%v) = %v, want %v", c.db, got, c.pos)
		}
	}
}

// TestFaderRoundTrip confirms position → dB → position lands within 0.05 dB
// across the usable range.
func TestFaderRoundTrip(t *testing.T) {
	for pos := 0.06; pos <= 1.0; pos += 0.02 {
		db := faderValueToDB(pos)
		back := dbToFaderValue(db)
		if !dbClose(pos, back, 0.05) {
			t.Errorf("round trip pos=%v → db=%v → pos=%v drifted beyond 0.05 dB", pos, db, back)
		}
	}
}

func dbClose(a, b, tolDB float64) bool {
	da := faderValueToDB(a)
	db := faderValueToDB(b)
	if math.IsInf(da, -1) && math.IsInf(db, -1) {
		return true
	}
	return math.Abs(da-db) <= tolDB
}
