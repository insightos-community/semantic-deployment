# Main deployment baseline before cuRobo

This baseline records deployed Skill packages and Ability 0.5.14, the SDK trajectory bridge and the 8 MiB Pilot message limit. The standalone repositories under .integration/current are archived separately; the managed SDK source is semantic-robotsdk/robot-sdk.

The main deployment keeps its original branches. The sibling semantic-isaac-curobo workspace contains source worktrees on feature/curobo-native. It has no running Server or Runtime, credentials, database, model weights or scene assets. Git LFS assets may need materialization before a new build.

The unpublished local grasp posture experiment is excluded. Source recipe versions and deployed package identities are recorded in manifest.json. This operation did not launch services or execute physical actions.
