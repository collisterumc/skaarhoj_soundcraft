package main

import "math"

// Fader value conversions ported from soundcraft-ui
// (packages/mixer-connection/src/lib/utils/value-converters/value-converters.ts).
// Wire fader values are linear
// positions 0.0–1.0; these map between that position and a dB reading for
// display labels.

// faderToLinearAmplitude is the mixer fader transfer function: position → linear
// amplitude. Below 0.055 the curve is shaped by a sine term.
func faderToLinearAmplitude(v float64) float64 {
	head := 1.0
	if v < 0.055 {
		head = math.Sin(28.559933214452666 * v)
	}
	poly := (23.90844819639692 + (-26.23877598214595+(12.195249692570245-0.4878099877028098*v)*v)*v) * v
	return head * math.Exp(poly) * 2.676529517952372e-4
}

// faderToLinearAmplitudeDeriv is the derivative used by Newton's method in
// dbToFaderValue.
func faderToLinearAmplitudeDeriv(v float64) float64 {
	p := (23.90844819639692 + (-26.23877598214595+(12.195249692570245-0.4878099877028098*v)*v)*v) * v
	pPrime := 23.90844819639692 + (-52.4775519642919+(36.58574907771074-1.9512399508112392*v)*v)*v
	expP := math.Exp(p)
	if v < 0.055 {
		wv := 28.559933214452666 * v
		return 2.676529517952372e-4 * expP * (28.559933214452666*math.Cos(wv) + math.Sin(wv)*pPrime)
	}
	return 2.676529517952372e-4 * expP * pPrime
}

// faderValueToDB converts a linear fader position to dB, rounded to 0.1 dB.
// Positions with negligible amplitude return -Inf.
func faderValueToDB(value float64) float64 {
	lin := faderToLinearAmplitude(value)
	if lin < 1e-10 {
		return math.Inf(-1)
	}
	db := math.Round(20*math.Log10(lin)*10) / 10
	if db == 0 {
		return 0 // normalize -0 to 0, matching the JS `|| 0`
	}
	return db
}

// dbToFaderValue inverts the transfer function with Newton's method, clamped to
// [0,1]. It mirrors the reference rounding to 11 decimals so transcendental
// rounding differences across platforms do not leak into wire values.
func dbToFaderValue(dbValue float64) float64 {
	if dbValue <= -200 {
		return 0
	}
	if dbValue >= 10 {
		return 1
	}
	target := math.Pow(10, dbValue/20)
	v := 0.5
	for i := 0; i < 20; i++ {
		delta := (faderToLinearAmplitude(v) - target) / faderToLinearAmplitudeDeriv(v)
		v -= delta
		if v < 0 {
			v = 1e-10
		}
		if v > 1 {
			v = 1
		}
		if math.Abs(delta) < 1e-15 {
			break
		}
	}
	return math.Round(v*1e11) / 1e11
}
