# ADR 0028 — spool ownership and disk reserve (Phase 6.4)

Status: implemented in `develop`, pending full CI and hardware validation.

## Ownership
`spool.Open` acquires a non-blocking exclusive advisory lock on the
persistent private file `.aegis.lock` using flock on Linux/macOS
or LockFileEx on Windows. The lock is held until Queue.Close; another
instance returns ErrLocked before scanning the directory. OS process
termination releases locks automatically; do not delete the lock file.
Symlinks, nonregular lock files and unsafe Unix permissions are rejected.
The CLI closes the queue on every return path. Windows ACLs must be
reviewed by the operator.

## Resource safety
The existing per-record limit (256 KiB) and queue quota remain enforced,
without eviction. Available filesystem free space must exceed the
new record size by at least 16 MiB; Linux/macOS use statfs and Windows
GetDiskFreeSpaceEx. Failure to query space stops the append.
This check is necessarily racy with other filesystem writers, so actual
I/O exhaustion at create/write/fsync/rename also returns an explicit
ErrDiskSpace error when recognized. Failed/partial writes never
alter the in-memory queue or delete existing records. Temporary files
are unlinked on failure and abandoned temporary files are removed on
the next exclusive Open.

Successful writes are fsynced and renamed in the spool directory.
Directory sync remains best effort for portability; abrupt power loss
cannot be promised to be zero-loss on every filesystem. A remote
durable database ACK remains required for removal.

## Testing and boundaries
Tests cover same-process and subprocess contention, Close/reopen,
symlink attack, injected low free space, mid-write ENOSPC and restart
after failure. CI targets Linux, macOS and Windows (race detector and
CGO-disabled build). Advisory locks assume cooperating writers on a
local filesystem; they are not a security boundary against malicious
same-user processes, directory replacement or unreliable network FS.
Actual disk-failure and Windows ACL tests on real machines remain open.
