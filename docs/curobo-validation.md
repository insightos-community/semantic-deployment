# cuRobo integration validation — 2026-09-24

Workspace: `/home/amax/dyc/semantic-isaac-curobo`, branch `feature/curobo-native`.
Main and test service installations remain unchanged.

## Automated checks

- Existing atomic controller and physical-limit lifecycle tests: 43 passed.
- New cuRobo command tests: 10 passed. They use a controlled numerical planner to
  check plan-only behavior, sample progression, cancellation, stopping all participating
  joints, resource exclusion, stale start, invalid trajectories and episode completion.
- Ability mapping and Skill candidate tests: 16 passed, plus 15 unittest subtests.
- Engine lifecycle tests: 6 passed after adding the pre-Isaac SDK bootstrap.
- R1Pro SDK Wheel builds successfully with the new planner modules.

## Native validation notes

An isolated process on GPU 3 loads `turning_on_radio`, native instance 301, from the
shared datasets. It submits commands through `AtomicSession`, the Runtime atomic
execution path. No Server task is created by this validation.

The first test-script attempt failed on tuple assignment while constructing a target.
The script was corrected. The next attempt exposed mixed Warp imports: pip Warp 1.12.1
was combined with Isaac's Warp 1.8.2 `context` and `torch` modules. SDK bootstrap now
preloads compatible modules before importing Isaac, and rejects a mixed-module process
with an explicit startup hint.

Final native results are recorded separately in the workspace `validation/` evidence.
Complete grasp/close/lift and held-object physical behavior remain separate validation
steps. Small endpoint motion cannot establish native task success or whole-scene
collision-avoidance coverage.

## Native direct endpoint result

On the isolated radio-301 scene, move the current right EEF upward by 0.03 m,
keeping its quaternion. Native cuRobo plans 31 samples at 1/30 s (1.0 s motion).
Plan-only succeeds in 17.88 s numerical wall time. Execution replanning takes
17.77 s; all 31 samples execute through Runtime's physics loop. Measured final
position error is 2.89e-6 m and orientation error 1.16e-5 rad. Torso joints and
the selected arm both move; the other arm remains held. Command status is succeeded.

This confirms the native planning-to-execution chain for this small free-space goal.

## Native constrained approach result

The second target uses a 0.03 m pre-approach offset opposite the tool extension axis.
The goal-frame constraint supports its tilted direction. Native plan-only succeeds in
53.76 s; execution replanning takes 33.27 s. The full 62-sample trajectory (2.033 s
simulation time) executes and completes. Measured final position error is 2.95e-6 m
and orientation error 1.44e-5 rad. This tests the plan_grasp approach stitching used
by the Skill with full collision links enabled, without closing on an object.

Evidence: [direct endpoint](curobo-evidence/direct-endpoint.json) and
[constrained approach](curobo-evidence/constrained-approach.json).
The isolated validation process has been stopped. Main/test Runtime and component
registrations were not switched by this integration.
