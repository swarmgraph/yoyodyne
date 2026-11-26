# The tracker server's launch, checked against a real dolt

Checking the tracker server's launch against a real dolt install
(yoyodyne-ifd.428.126.4.42) is one of the things switching the server on
(yoyodyne-ifd.428.126.4.41) waits for. This is its record. All four checks
passed. The hold that kept the server off (`beads.ServerHold`) was left as it
was by this check and emptied by switching the server on
(yoyodyne-ifd.428.126.4.41). The hold asked for the copy `yoyo setup` installs;
the Lead Product Manager decided on November 26, 2026 that this record, made on
Homebrew's copy of the same version, meets it, because what was checked depends
on dolt's version and not on where it was installed from.

## What was run

- **When and where:** November 25, 2026, about 12:55 PST, on macOS (Apple
  silicon), inside a developer run's sandbox.
- **dolt:** `dolt version 2.7.6`, installed by Homebrew at
  `/opt/homebrew/bin/dolt`. That is the version the harness pins. It is not the
  copy `yoyo setup` downloads, which was not installed on this machine.
- **Command:** `go test -trimpath ./internal/supervise -count=1 -v -run TestARealDolt`

The test is `internal/supervise/trackerserver_realdolt_test.go`. It makes a
store with one dolt database in a temporary directory, and has the supervisor's
own code (`internal/supervise/trackerserver.go`) make the account, build the
arguments and the environment, start the server through the launcher the
product uses, and stop it. Two things differ from the product: the program is
the dolt found on the path, and a shell sets a two-minute deadline before
becoming dolt, so a test that dies leaves no server running. The connections
are made with dolt's own client, as the server's one account. The test does not
run the tracker command and does not touch the live store.

## Output

The test never prints the password: anything dolt says is masked as
`(password)` before it is logged, and nothing in this output needed masking.

```
=== RUN   TestARealDoltServerRefusesAConnectionWithoutItsPasswordAndLeavesNoSecret
    trackerserver_realdolt_test.go:79: dolt: /opt/homebrew/bin/dolt, dolt version 2.7.6
    trackerserver_realdolt_test.go:89: launched: /opt/homebrew/bin/dolt sql-server --data-dir /private/tmp/claude-501/yoyodyne-test-156727966/TestARealDoltServerRefusesAConnectionWithoutItsPasswordAndLeaves620867231/001/repository/.beads/embeddeddolt --host 127.0.0.1 --port 59171 --doltcfg-dir /private/tmp/claude-501/yoyodyne-test-156727966/TestARealDoltServerRefusesAConnectionWithoutItsPasswordAndLeaves620867231/001/state/services/tracker-server-calc/cfg --privilege-file /private/tmp/claude-501/yoyodyne-test-156727966/TestARealDoltServerRefusesAConnectionWithoutItsPasswordAndLeaves620867231/001/state/services/tracker-server-calc/privileges.db --branch-control-file /private/tmp/claude-501/yoyodyne-test-156727966/TestARealDoltServerRefusesAConnectionWithoutItsPasswordAndLeaves620867231/001/state/services/tracker-server-calc/branch_control.db --skip-root-user-initialization
    trackerserver.go:127: the tracker's server is running as process 11264 on port 59171
    trackerserver_realdolt_test.go:149: check 1, a connection without the password: refused=true: exit status 1: failed to connect to the dolt sql-server at 127.0.0.1:59171: Error 1045 (28000): Access denied for user 'yoyodyne'
    trackerserver_realdolt_test.go:149: check 2, a connection with a wrong password: refused=true: exit status 1: failed to connect to the dolt sql-server at 127.0.0.1:59171: Error 1045 (28000): Access denied for user 'yoyodyne'
    trackerserver_realdolt_test.go:162: check 3, a connection with the password: accepted=true: <nil>: word
        kept
    trackerserver_realdolt_test.go:207: check 4, secrets in the files under the store while the server ran: 0 found in 19 files
    trackerserver_realdolt_test.go:207: check 4, secrets in the files under the store after the server stopped: 0 found in 18 files
--- PASS: TestARealDoltServerRefusesAConnectionWithoutItsPasswordAndLeavesNoSecret (1.14s)
PASS
ok  	github.com/mason-bryant/yoyodyne/internal/supervise	1.897s
```

## Results

| Check | Result |
| --- | --- |
| 1. A connection without the password is refused | Passed: `Error 1045 (28000): Access denied` |
| 2. A connection with a wrong password is refused | Passed: `Error 1045 (28000): Access denied` |
| 3. A connection with the password is accepted | Passed: it created a table, committed it, and read the row back |
| 4. No file under the store holds the password once the server was used and stopped | Passed: 0 of 18 files |

Check 4 looked for the password as written, for the bytes it is the hex form of,
and for the secret dolt makes for its own local superuser. It looked while the
server ran as well as after, because dolt removes its own secret when it stops.
The same search finds the secrets in the server's own folder, so it can find
one.

The test was also seen to fail: with the account given an empty password, checks
1 and 3 failed; with the link that leads dolt's own secret out of the store
left out, the test stopped because the secret was not in the server's folder.
Both edits were undone. With dolt off the path it skipped, saying
`SKIPPED, not passed: dolt was not found`; that is not counted as a pass.

The forge's workflows install no dolt, so a passing check there is expected to
be that skip and is no evidence for these results. To make the check again, run
the command above on a machine with dolt on the path and look for `--- PASS`.
