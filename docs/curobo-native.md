# Native Runtime cuRobo integration

当前部署版本与迁移步骤见 [cuRobo 主部署迁移](curobo-main-deployment.md)。本页保留初期接口设计，当前执行细节以迁移说明和源码为准。

This branch loads the SDK planner in the Semantic Runtime's native Isaac process.
The SDK client in Ability submits the goal over the existing atomic-command HTTP API.
Pilot and Server keep their current invocation protocol. Runtime owns physics stepping,
scene lifecycle, command occupancy, stop and native episode evaluation.

## Source boundaries

- `semantic-robotsdk/robot-sdk/packages/r1pro`: `utils/curobo_motion.py` imports the
  native OmniGibson generator lazily, updates real obstacle geometry, specializes the
  robot model and solves. `utils/curobo_job.py` schedules numerical planning in one
  worker and yields successive samples to the host. Neither module starts a simulator.
- `semantic-simulation/isaac-runtime`: the atomic controller loads SDK jobs, records
  command state, samples measured EEF completion and handles cancellation/episode end.
- `semantic-ability/r1pro-behavior-ability`: existing MoveEndEffector forwards options.
- `semantic-skill/robot-skill`: behavior-grasp's cuRobo mode generates task geometry,
  tries plan-only candidates, executes the chosen approach, confirms contact and lifts.

The original WangWei implementation is the source for the SDK planner adapter.
Its extra joint-bound cost overrides are removed. Native collision constraints and
physical model limits remain. Finger/camera collision spheres cover installed meshes;
only same-wrist fixed-assembly internal overlaps are ignored. Other-arm/torso/world
checks remain enabled. `use_torso=true` moves one seven-joint arm plus the four torso
joints. Base and the other arm are held during planning/execution.

## Existing endpoint additions

`motion.move_end_effector` / `sdk.eef.control_single_arm` accept:

- `motion_mode`: `ik` (existing default) or `curobo`.
- `plan_only`: return a feasible plan summary without executing its trajectory.
- `use_torso`: include torso joints (default true).
- `approach_offset`: optional pre-approach displacement from the target, in the same
  reference frame and metres. The final segment preserves the goal orientation.
- `attached_object_ref`: confirmed held object, represented in carried collision geometry.

The target frame is resolved to world at submission. The planner transforms to its
base frame and converts xyzw to wxyz. An approach axis aligned with either the planner
base or target-local axes is supported. Oblique-to-both axes returns an explicit error.
Stop keeps gripper preload and stops the whole planned torso/arm command. A numerical
job that is already running finishes in the background; its cancelled result is discarded.
Until it finishes, the same planner cannot be reused.

Collision/contact policy: all world collision links stay enabled during approach.
Geometric enclosure permits the object in the open finger gap. Closing and force/contact
confirmation remain in the gripper Ability. After confirmation, a held single-link object
or a connected assembly of enabled USD fixed joints is attached for carry planning.
All enabled collision meshes from all its links are transformed into the common planning
frame, merged into EEF-local attachment spheres, and disabled in the world collision checker
while attached. Detach restores those world obstacles; the next snapshot after release reads
the object's current pose. The single-link mesh fitting path is unchanged.
Movable joints, disconnected links and joints anchored outside the object are rejected;
the adapter does not freeze joints to make them appear rigid. Fixed metadata links without
collision geometry are allowed without inventing additional meshes. General articulated
held objects and pair-specific intentional contact exclusions need further work.

## Grasp mode

The grasp Skill uses cuRobo for all arm motion in this branch.
Observation posture and perception are retained. Operation torso is `auto`, or null to
hold its current joints. Observation torso angles remain available; operation torso uses auto or null.
The Skill checks enclosure, orders candidate EEF directions by task preference and
asks Runtime to plan. `side:auto` compares candidates from both arms. It selects the
first feasible candidate in that order; this is not a globally optimal side comparison.
Actual execution reuses the prepared trajectory when command intent and measured starting joints match; otherwise the SDK plans from the measured start. Planning failures do not fall
back to unchecked IK. The full path is never stored in a Skill checkpoint.
The legacy grasp execution switch and full trajectory checkpoint fields have been removed. Candidate direction
sampling and enclosure remain task geometry. Existing observation-posture computation
remains separate. After contact confirmation, ordinary IK lifts 1 cm with a 3 mm readback tolerance, followed by cuRobo carrying motion; initial approach feasibility
does not assert successful grasp, lift or native task success.

## Native launch

Use `scripts/run-curobo-runtime.sh` from this workspace; it adds the matching SDK/core
and Runtime sources to the native Python process. Provide:

```bash
export SEMANTIC_BEHAVIOR_PYTHON=/absolute/path/to/native-env/bin/python
export OMNIGIBSON_DATA_PATH=/absolute/path/to/BEHAVIOR-1K/datasets
export SEMANTIC_BEHAVIOR_GPU=<selected GPU>
export SEMANTIC_RUNTIME_PORT=<unused port>
./semantic-robot-deployment/scripts/run-curobo-runtime.sh
```

The launcher sets `SEMANTIC_CUROBO=1`. Before Isaac import, Runtime calls the SDK's
Warp bootstrap, preloading the chosen Warp's public compatibility modules. This avoids
mixing pip Warp with Isaac's bundled Warp submodules in the same process.

The Python environment needs the matching native Isaac/OmniGibson/cuRobo stack.
Ability must load the SDK from the same branch. This launcher does not install or switch
main/test components. Container packaging is outside this native integration validation;
its engine also needs these SDK modules before enabling cuRobo commands.

## Validation

See the workspace integration evidence for native scene planning/execution results.
Mocked command tests cover no-motion planning, continuous sample progression, pending
cancellation, hold across torso/arm, resource exclusion and failed-plan handling. Ability
and Skill tests cover option propagation and selecting another no-motion candidate after
an explicitly failed collision plan. These tests do not establish physical task success.

## Endpoint-only Skill revision

Grasp, initialization, radio-button and placement arm actions now submit EEF goals
through cuRobo. Radio natural/retract/flip/observe/button stages retain their order;
each sends one Cartesian goal. The flip constrains the final 180-degree orientation.
The SDK selects intermediate motion and joint posture. Skill checkpoints retain
endpoint goals and results. Torso-only actions keep their existing target-angle and
duration interface. Offline endpoint geometry still includes natural reference and
camera field-of-view calculation. Whole-path generation runs in SDK/Runtime.

Validation: 111 relevant tests passed across workflow sequencing, failure/restore,
mirrored goals, natural observation geometry, initialization and placement. Four
changed Skill manifests, entries, input examples and state models load successfully.
This revision has not been installed into main/test registries or physically replayed.
