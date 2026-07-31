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

	e.logf("== full cycle: break all, fix all, verify")
	if err := e.Break(ctx); err != nil {
		return err
	}
	if err := e.sleep(ctx, e.settleFor(faults...)); err != nil {
		return err
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
	if e.script(ctx, f.Script("check.sh"), nil) == nil {
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
		err := e.script(ctx, f.Script("check.sh"), nil)
		if err == nil {
			e.logf("-- %s: fixed", id)
			return nil
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
