# semantic-deployment: reproducible platform builds

## Versions, tools and build layout

The glibc installer pins component tag **v0.5.0-insightos.2026.2** at `70c4a025c5995f8ad7b8770718b73932ef57febf`.
This guide pins the current build-script snapshot at `cabd8f847670c589d46ca475dcb0b12a85d78c6a`.
To reconstruct another published release, read its `release.json` and select
both `source_commit` and `build_recipe_commit`; a source tag alone may predate
the CI scripts. This recipe reproduces the build steps, not historical archive bytes.

Prerequisites: Linux x86_64, Go 1.25.8, GCC for race tests, binutils and Python 3.

The release scripts expect **two sibling checkouts**, `automation/` for build
scripts and `source/` for the component. Run these commands from a fresh working
directory (the scripts themselves are not standalone copies):

```bash
REPRO_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/semantic-deployment-repro.XXXXXXXX")"
git clone --no-checkout https://github.com/insightos-community/semantic-deployment.git "$REPRO_ROOT/automation"
GIT_LFS_SKIP_SMUDGE=1 git -C "$REPRO_ROOT/automation" checkout --detach cabd8f847670c589d46ca475dcb0b12a85d78c6a
git clone --no-checkout https://github.com/insightos-community/semantic-deployment.git "$REPRO_ROOT/source"
GIT_LFS_SKIP_SMUDGE=1 git -C "$REPRO_ROOT/source" checkout --detach v0.5.0-insightos.2026.2
cd "$REPRO_ROOT/source"
test "$(git rev-parse HEAD)" = 70c4a025c5995f8ad7b8770718b73932ef57febf
export TARGET_TAG=v0.5.0-insightos.2026.2
export COMPONENT=semantic-deployment
export GITHUB_SHA=cabd8f847670c589d46ca475dcb0b12a85d78c6a
```

## Linux glibc / standard component Release

The executable build entry is [`.github/scripts/build.sh`](.github/scripts/build.sh);
archive validation is [`.github/scripts/package.py`](.github/scripts/package.py).
From `source/` in the layout above:

```bash
bash ../automation/.github/scripts/build.sh
python3 ../automation/.github/scripts/package.py 
(cd .output/release && sha256sum -c SHA256SUMS)
```

Artifacts: `source/.output/release/` (archives/wheels, `release.json`, checksum
inventory and license notices). `release.json` records source and recipe revisions.
The local commands do not publish or overwrite a GitHub Release.

The applications use `CGO_ENABLED=0`, `netgo,osusergo` and are checked for missing
ELF interpreter/NEEDED entries. These static binaries work in both Linux variants;
the Robot Python environment is a separately assembled dependency set.

## Linux musl

Reuse the static Linux component from the standard build above. The glibc
packager performs host race tests and readelf checks; do not invoke a CGO race
build inside Alpine and assume it is the release binary.

For the complete musl build and offline checks, use the [quick-start musl commands](https://github.com/insightos-community/quick-start/blob/main/README.build.md#linux-musl-x86_64).

## macOS / macosx

Use Apple Silicon arm64 and the native adaptation at `f6256f7a4e77eda63b683cfb031af5fcf52c413e`;
the older glibc component tag above may not contain the macOS fixes. Start a
separate checkout and run the native commands:

```bash
git clone https://github.com/insightos-community/semantic-deployment.git semantic-deployment-macos
cd semantic-deployment-macos
git checkout --detach f6256f7a4e77eda63b683cfb031af5fcf52c413e
test "$(uname -s)" = Darwin
test "$(uname -m)" = arm64
```

Prerequisite: Go 1.25.8.

```bash
go test ./... -count=1 -timeout=5m
mkdir -p dist/bin
for name in semantic-robot-instance semantic-robot-bundle; do
 CGO_ENABLED=0 go build -trimpath -o "dist/bin/$name" "./cmd/$name"
done
tar -czf dist/semantic-deployment-macos-arm64.tar.gz -C dist/bin .
```

The native workflow is [`.github/workflows/macos.yml`](.github/workflows/macos.yml).
Its artifacts are component development outputs; quick-start assembles and validates
the complete installer.

The complete macOS installer targets Apple Silicon/macOS 15.5+; see the [locked assembly instructions](https://github.com/insightos-community/quick-start/blob/main/README.build.md#macos-apple-silicon).

## GitHub workflow reproduction

The repository’s [CI workflow](.github/workflows/ci.yml) implements the two-checkout
layout. To build a source tag without publishing, create a reproduction branch at the
pinned automation commit. GitHub dispatch expects a branch/tag ref; both tag refs
and default-branch dispatches can enter this workflow’s publishing job. The following
commands require repository write access and use a non-default branch:

```bash
gh auth setup-git
REPRO_BRANCH=reproduce/platform-builds
git -C "$REPRO_ROOT/automation" push origin cabd8f847670c589d46ca475dcb0b12a85d78c6a:refs/heads/$REPRO_BRANCH
gh workflow run ci.yml --repo insightos-community/semantic-deployment --ref "$REPRO_BRANCH" -f tag=v0.5.0-insightos.2026.2
gh run list --repo insightos-community/semantic-deployment --workflow ci.yml --limit 5
# Set REPRO_RUN_ID to the selected run ID.
gh run watch "$REPRO_RUN_ID" --repo insightos-community/semantic-deployment --exit-status
gh run download "$REPRO_RUN_ID" --repo insightos-community/semantic-deployment --name release-assets --dir downloaded-release
```

```bash
git -C "$REPRO_ROOT/automation" push origin f6256f7a4e77eda63b683cfb031af5fcf52c413e:refs/heads/reproduce/macos
gh workflow run macos.yml --repo insightos-community/semantic-deployment --ref reproduce/macos
```

## Reproduction evidence

Build in a fresh checkout and a separate output directory for each ABI. Preserve
source commits, compiler/tool versions, dependency locks, package inventories and
test logs. Fixed source revisions and a container digest reproduce the recipe;
unlocked OS packages, runner images, timestamps and build tools can still change
archive bytes. Compare a downloaded release against its published `SHA256SUMS`;
do not expect a local rebuild to have the same digest.

See the [complete installer and repository index](https://github.com/insightos-community/quick-start/blob/main/README.build.md) for assembly order,
platform locks and end-to-end validation. Local build commands do not publish a
Release. Publishing requires repository write access and a new version tag;
existing release tags/assets should not be replaced.
