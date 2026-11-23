"""One browser slot shared with internal/scrape/browser_slot.go."""
import asyncio
from contextlib import asynccontextmanager, contextmanager
import fcntl
import os
import stat
import sys
import time
from pathlib import Path


def _open_lock():
    uid = os.geteuid()
    directory = Path(f"/tmp/tts-scraper-browser-{uid}")
    directory.mkdir(mode=0o700, exist_ok=True)
    info = directory.lstat()
    if not stat.S_ISDIR(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o700 or info.st_uid != uid:
        raise RuntimeError("browser lock directory must be owned by the scraper account with mode 0700")
    return os.open(directory / "browser.lock", os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW | os.O_NONBLOCK, 0o600)


def _try_lock(fd):
    try:
        fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except BlockingIOError:
        return False
    info = os.fstat(fd)
    if not stat.S_ISREG(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o600 or info.st_uid != os.geteuid():
        raise RuntimeError("browser lock must be a private regular file owned by the scraper account")
    if info.st_size:
        raise RuntimeError("browser slot has an unfinished lease; verify browser cleanup before clearing the lock file")
    os.write(fd, f"{os.getpid()}\n".encode())
    return True


class _Lease:
    def __init__(self):
        self.fd = _open_lock()
        self.clean = False

    def close(self):
        try:
            if self.clean:
                os.ftruncate(self.fd, 0)
        finally:
            os.close(self.fd)


@asynccontextmanager
async def guarded_async_browser(factory):
    lease = _Lease()
    try:
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            if _try_lock(lease.fd):
                break
            await asyncio.sleep(0.05)
        else:
            raise TimeoutError("wait for browser slot timed out")
        lease.clean = True
        manager = factory()
        lease.clean = False
        try:
            browser = await manager.__aenter__()
        except BaseException as launch_error:
            # Startup can fail after spawning a driver. Preserve the launch error.
            try:
                await manager.__aexit__(*sys.exc_info())
                lease.clean = True
            except BaseException as cleanup_error:
                launch_error.add_note(f"Browser cleanup failed: {cleanup_error}")
            raise
        try:
            yield browser
        except BaseException:
            suppressed = await manager.__aexit__(*sys.exc_info())
            lease.clean = True
            if not suppressed:
                raise
        else:
            await manager.__aexit__(None, None, None)
            lease.clean = True
    finally:
        lease.close()


@contextmanager
def guarded_sync_browser(factory):
    lease = _Lease()
    try:
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            if _try_lock(lease.fd):
                break
            time.sleep(0.05)
        else:
            raise TimeoutError("wait for browser slot timed out")
        lease.clean = True
        manager = factory()
        lease.clean = False
        try:
            browser = manager.__enter__()
        except BaseException as launch_error:
            # Startup can fail after spawning a driver. Preserve the launch error.
            try:
                manager.__exit__(*sys.exc_info())
                lease.clean = True
            except BaseException as cleanup_error:
                launch_error.add_note(f"Browser cleanup failed: {cleanup_error}")
            raise
        try:
            yield browser
        except BaseException:
            suppressed = manager.__exit__(*sys.exc_info())
            lease.clean = True
            if not suppressed:
                raise
        else:
            manager.__exit__(None, None, None)
            lease.clean = True
    finally:
        lease.close()
