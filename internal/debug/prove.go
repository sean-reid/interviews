package debug

import (
	"context"
	"fmt"
	"time"
)

// Prove is the anti-rot gate: on a healthy environment, every fault must
// break the check when injected and converge to fixed after its documented
// fix; then the full pack breaks and recovers end to end. Content that
// merges is content that still works.
func (e *Engine) Prove(ctx context.Context) error {
	pack, err := e.Pack()
	if err != nil {
		return err
	}
	faults := e.Scenario.PackFaults(pack)
	if len(faults) == 0 {
		return fmt.Errorf("pack %q has no faults", pack)
	}

	e.logf("== prove %s pack %s: %d faults", e.Variant.Problem, pack, len(faults))
	if err := e.Up(ctx); err != nil {
		return err
	}

	for _, f := range faults {
		if err := e.proveFault(ctx, f); err != nil {
			return err
		}
	}

	e.logf("== full cycle: break all, verify fails, fix all, verify passes")
	if err := e.Break(ctx); err != nil {
		return err
	}
	if err := e.sleep(ctx, e.settleFor(faults...)); err != nil {
		return err
	}
	// The pack has to take the app down. Without this the whole gate passes
	// for a scenario whose verify script cannot fail, which makes every
	// per-fault check the only thing standing between a rotted exercise and
	// a green build.
	if err := e.Verify(ctx); err == nil {
		return fmt.Errorf("the whole pack is injected and verify still passes: verify does not detect this scenario breaking")
	}
	if err := e.Fix(ctx, ""); err != nil {
		return err
	}
	if err := e.VerifyWait(ctx); err != nil {
		return fmt.Errorf("environment did not recover after fixing the full pack: %w", err)
	}
	e.logf("== pack %s: all green", pack)
	return nil
}

func (e *Engine) proveFault(ctx context.Context, f Fault) error {
	id := f.Spec.ID
	e.logf("-- %s: inject", id)
	if err := e.script(ctx, f.Script("inject.sh"), nil); err != nil {
		return fmt.Errorf("%s: inject failed: %w", id, err)
	}
	if err := e.sleep(ctx, e.settleFor(f)); err != nil {
		return err
	}
	// A check that cannot run fails like a present fault, and a gate that
	// takes that for broken-as-expected goes on to blame the documented fix.
	switch e.check(ctx, f) {
	case CheckCannotRun:
		return cannotRunErr(f)
	case CheckFixed:
		return fmt.Errorf("%s: check passes immediately after inject; the fault does not break anything", id)
	}
	e.logf("-- %s: broken as expected, fixing", id)
	if err := e.script(ctx, f.Script("fix.sh"), nil); err != nil {
		return fmt.Errorf("%s: fix failed: %w", id, err)
	}
	timeout := e.FixTimeout
	if f.Spec.FixTimeoutSeconds > 0 {
		timeout = time.Duration(f.Spec.FixTimeoutSeconds) * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		switch e.check(ctx, f) {
		case CheckFixed:
			e.logf("-- %s: fixed", id)
			return nil
		case CheckCannotRun:
			return cannotRunErr(f)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s: still broken %s after the documented fix", id, timeout)
		}
		if err := e.sleep(ctx, e.PollInterval); err != nil {
			return err
		}
	}
}

// cannotRunErr names the script, because the fix is to that script and
// nothing about the environment says which one it was.
func cannotRunErr(f Fault) error {
	return fmt.Errorf("%s: %s exited %d, so the check itself could not run; fix the check script",
		f.Spec.ID, f.Script("check.sh"), CheckCannotRunExit)
}

// settleFor is the longest settle among the given faults, floored by the
// engine default.
func (e *Engine) settleFor(faults ...Fault) time.Duration {
	settle := e.Settle
	for _, f := range faults {
		if d := time.Duration(f.Spec.SettleSeconds) * time.Second; d > settle {
			settle = d
		}
	}
	return settle
}
