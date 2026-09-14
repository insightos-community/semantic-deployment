# Process platform contracts

`process.Start` isolates each owned child tree. Unix uses a process group; Windows
creates a suspended process, assigns a Job Object, then resumes the primary thread.
Windows children cannot run or spawn descendants before assignment. Start failures
reap the child and release handles. Windows uses the documented Toolhelp thread
enumeration and ResumeThread APIs with the Go-owned `exec.Cmd` wait lifecycle.

`Tree.Close` only releases ownership handles. It must not terminate processes:
when Robot safety is unconfirmed, the supervisor preserves Pilot/AbilityFramework
for reconciliation. `Tree.Terminate`/`Kill` are explicit retirement operations,
allowed only after the caller's safety protocol. Windows termination is forced
job termination, not a substitute for a graceful application stop.

`stop.NotifyContext` and `stop.Request` implement graceful application shutdown.
Unix retains SIGTERM; Windows uses a local named event protected by the default
process-token DACL. The name includes PID and process creation time. Requests
without a matching Windows identity are rejected and never fall back to killing.
Windows supervisor state records this identity. Semantic Pilot must implement the
same event protocol before a complete Windows Robot runtime can be supported.

Tests run native helper executables without a shell on Linux, macOS and
Windows Server 2022, including paths containing spaces and Unicode. These tests
validate platform contracts and command builds, not the complete physical Robot
or installer. Full Windows application/graphical testing remains required.

```text
go test ./internal/ports/... -count=1 -timeout=2m
go build ./cmd/...
```

API references: [Job Objects](https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects),
[AssignProcessToJobObject](https://learn.microsoft.com/en-us/windows/win32/api/jobapi2/nf-jobapi2-assignprocesstojobobject),
[ResumeThread](https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-resumethread),
[event security](https://learn.microsoft.com/en-us/windows/win32/sync/synchronization-object-security-and-access-rights).
