package coder

import "testing"

// The fixed cap conflated two situations identical by turn count and completely
// different by behaviour: a runaway loop and legitimately long work. Budget is spent
// only by turns that achieved nothing, so real work runs on while a model spinning
// on one failing call still dies quickly.
func TestTurnBudgetProductiveWorkRunsPastTheBase(t *testing.T) {
	b := newTurnBudget(false) // run/chat: base 30
	for i := 0; i < 100; i++ {
		if stop, reason := b.next(true); stop {
			t.Fatalf("productive turn %d stopped early (%s)", i, reason)
		}
	}
}

func TestTurnBudgetStopsOnUnproductiveStreak(t *testing.T) {
	b := newTurnBudget(false)

	// Interleaving keeps the streak broken, so the run continues.
	for i := 0; i < 20; i++ {
		if stop, _ := b.next(i%2 == 0); stop {
			t.Fatalf("interleaved turn %d stopped early", i)
		}
	}

	// Six dead turns in a row is a model going nowhere. Stop sooner than the base
	// budget would — waiting out 30 turns of failure helps nobody and costs money.
	var stop bool
	var reason string
	for i := 0; i < 6; i++ {
		stop, reason = b.next(false)
	}
	if !stop || reason != "unproductive" {
		t.Fatalf("after 6 dead turns: stop=%v reason=%q, want true/unproductive", stop, reason)
	}
}

func TestTurnBudgetSpendsBaseOnUnproductiveTurnsOnly(t *testing.T) {
	b := newTurnBudget(false) // base 30
	spent := 0
	for spent < 29 {
		b.next(false)
		spent++
		if spent%5 == 0 {
			b.next(true) // resets the streak, spends no base budget
		}
	}
	stop, reason := b.next(false) // the 30th unproductive turn
	if !stop || reason != "budget" {
		t.Fatalf("stop=%v reason=%q, want true/budget", stop, reason)
	}
}

func TestTurnBudgetHardCeilingIsNeverExtended(t *testing.T) {
	b := newTurnBudget(true) // build: base 50
	var stop bool
	var reason string
	for i := 0; i < 500 && !stop; i++ {
		if stop, reason = b.iterate(); stop {
			break
		}
		stop, reason = b.next(true) // always productive — only the ceiling can stop this
	}
	if reason != "hard-ceiling" {
		t.Fatalf("reason = %q, want hard-ceiling", reason)
	}
}

// The ceiling must bind even when a turn never reaches next() — the shape of the
// tools-unsupported degrade and the verify-finish nudge, both of which `continue`.
// Without iterate() counting at the top, such a path would spin forever.
func TestTurnBudgetCeilingBindsWithoutOutcomes(t *testing.T) {
	b := newTurnBudget(false)
	var stop bool
	var reason string
	for i := 0; i < 1000 && !stop; i++ {
		stop, reason = b.iterate() // never calls next(), as a `continue` path would not
	}
	if !stop || reason != "hard-ceiling" {
		t.Fatalf("stop=%v reason=%q, want true/hard-ceiling — an outcome-free loop must still terminate", stop, reason)
	}
}

func TestTurnBudgetBuildBaseExceedsRunBase(t *testing.T) {
	// A build carries the same work PLUS verify nudges and the grace turn, so its
	// base must never be the smaller of the two.
	if newTurnBudget(true).base <= newTurnBudget(false).base {
		t.Fatal("build base budget must exceed the run base budget")
	}
}
