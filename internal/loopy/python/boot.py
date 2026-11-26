# SPDX-License-Identifier: MPL-2.0
# What an AutoLink starts with: it talks to 1loop over its standard input
# and output, keeps the script to its folder and its sites, then runs it.
import sys, os, json, io

_job = json.loads(sys.stdin.buffer.readline())
_in = os.fdopen(os.dup(0), "rb")
_out = os.fdopen(os.dup(1), "wb", buffering=0)
os.dup2(2, 1)  # whatever a library prints by itself goes with the errors
_null = os.open(os.devnull, os.O_RDONLY)
os.dup2(_null, 0)
os.close(_null)

import threading, socket, builtins, types, traceback  # before the guard

_lock = threading.Lock()
_write = threading.Lock()


def _send(message):
    data = json.dumps(message, ensure_ascii=False, default=str).encode("utf-8", "replace") + b"\n"
    with _write:
        _out.write(data)


class AutoLinkError(Exception):
    """1loop refused, or could not do it."""


def _call(name, /, **args):
    with _lock:  # one at a time: the answer is the next line
        _send({"call": name, "args": args})
        line = _in.readline()
    if not line:
        raise AutoLinkError("1loop is gone")
    answer = json.loads(line)
    if answer.get("error"):
        raise AutoLinkError(answer["error"])
    return answer.get("result")


class _Printed(io.TextIOBase):
    encoding = "utf-8"

    def writable(self):
        return True

    def write(self, text):
        if text:
            _send({"print": str(text)})
        return len(text)


def _guard(job):
    real = os.path.realpath
    norm = os.path.normcase
    reads = tuple(norm(real(p)) for p in (
        [sys.prefix, sys.base_prefix, sys.exec_prefix, job["lib"], job["dir"], job["data"], job["out"], job["boot"]]
        + [p for p in sys.path if p]
        + ["/etc/ssl", "/etc/pki", "/etc/ca-certificates", "/usr/share/ca-certificates", "/usr/share/zoneinfo", "/usr/lib/ssl",
           "/etc/resolv.conf", "/etc/hosts", "/etc/nsswitch.conf", "/etc/host.conf", "/etc/gai.conf", "/etc/services", "/etc/protocols",
           "/etc/localtime", "/etc/timezone", "/etc/mime.types", "/usr/share/mime", "/dev/null", "/dev/urandom", "/dev/random"]
        + ([job["ca"]] if job.get("ca") else [])))
    writes = tuple(norm(real(p)) for p in (job["data"], job["out"]))
    devices = ("/dev/null", "nul", os.devnull)
    sites = tuple(s.lower().lstrip(".") for s in job["sites"])
    anywhere = "*" in sites
    resolved = set()
    Refused = PermissionError

    def under(path, roots):
        if isinstance(path, int):
            return True
        try:
            path = norm(real(os.fsdecode(path)))
        except (TypeError, ValueError):
            return False
        return any(path == root or path.startswith(root.rstrip("/\\") + os.sep) for root in roots)

    def allowed(host):
        if anywhere:
            return True
        if isinstance(host, bytes):
            host = host.decode("ascii", "replace")
        host = str(host or "").lower().rstrip(".").strip("[]")
        return host in resolved or any(host == s or host.endswith("." + s) for s in sites)

    def no(what):
        raise Refused("1loop: this AutoLink may not " + what)

    def site(host):
        if not allowed(host):
            no("reach %s (its sites: %s)" % (host, ", ".join(sites) or "none"))

    blocked = ("os.system", "os.exec", "os.fork", "os.forkpty", "os.posix_spawn", "os.spawn", "os.startfile", "os.startfile/2",
               "subprocess.Popen", "pty.spawn", "os.symlink", "os.link", "os.chown", "os.kill", "os.killpg", "os.putenv", "winreg.ConnectRegistry",
               "winreg.OpenKey", "winreg.CreateKey", "winreg.SetValue", "winreg.DeleteKey", "ctypes.dlsym", "ctypes.dlsym/handle",
               "socket.bind", "syslog.openlog", "ftplib.connect", "webbrowser.open", "mmap.__new__", "sys.addaudithook", "_winapi.CreateProcess",
               "_winapi.OpenProcess", "cpython.run_command", "cpython.run_interactivehook", "gc.get_objects", "gc.get_referrers", "gc.get_referents")
    changes = ("os.remove", "os.rmdir", "os.mkdir", "os.chmod", "os.truncate", "os.utime", "os.mkfifo", "os.mknod", "os.setxattr", "os.removexattr",
               "shutil.rmtree", "shutil.chown")
    lists = ("os.listdir", "os.scandir", "os.walk", "os.chdir", "glob.glob")
    native = (os.sep + "lib-dynload" + os.sep,)

    def hook(event, args):
        if event == "import" and str(args[0]).split(".")[0] in natives:
            # Native calls reach memory, and the guard with it.
            raise ImportError("1loop: an AutoLink may not load native code (%s)" % args[0])
        if event == "open":
            path, mode, flags = args
            if path is None or isinstance(path, int):
                return
            writing = False
            if isinstance(mode, str):
                writing = any(c in mode for c in "wax+")
            elif isinstance(flags, int):
                writing = bool(flags & (os.O_WRONLY | os.O_RDWR | os.O_CREAT | os.O_TRUNC | os.O_APPEND))
            name = os.fsdecode(path) if not isinstance(path, str) else path
            if name.lower() in devices:
                return
            if writing:
                if not under(path, writes):
                    no("write %s: write in autolink.data or autolink.out" % name)
            elif not under(path, reads):
                no("read %s" % name)
        elif event in ("socket.getaddrinfo", "socket.gethostbyname", "socket.gethostbyaddr"):
            site(args[0])
        elif event in ("socket.connect", "socket.sendto"):
            address = args[1]
            if isinstance(address, tuple):
                site(address[0])
            else:
                no("use local sockets")
        elif event in blocked:
            if event == "mmap.__new__" or event == "os.putenv":
                return
            no("do %s" % event)
        elif event in changes:
            if not under(args[0], writes):
                no("change %s" % (args[0],))
        elif event == "os.rename":
            if not (under(args[0], writes) and under(args[1], writes)):
                no("move %s" % (args[0],))
        elif event in lists:
            path = args[0] if args and args[0] is not None else os.getcwd()
            if event in ("os.listdir", "os.scandir", "os.chdir") and not under(path, reads):
                no("list %s" % (path,))
        elif event.startswith("ctypes."):
            # Each native call, address and symbol: ctypes stays inert.
            no("use native code (%s)" % event)

    # Native modules that start programs or reach memory are made by these:
    # refused there, whatever way the import takes. Python cannot be closed
    # from inside: this stops mistakes and plain attempts, not a script
    # written to undo it. What runs is what the user allowed on a card.
    import _imp
    natives = ("_ctypes", "ctypes", "_cffi_backend", "cffi", "_testcapi", "_testinternalcapi", "_xxsubinterpreters", "_interpreters")
    starters = {"_posixsubprocess": ("fork_exec",), "_winapi": ("CreateProcess", "OpenProcess")}

    def refused(*_, **__):
        no("start a program")

    def tame(module):
        for name in starters.get(getattr(module, "__name__", ""), ()):
            if hasattr(module, name):
                setattr(module, name, refused)
        return module

    def guarded(real):
        def create(spec, *args):
            if spec.name.split(".")[0] in natives:
                raise ImportError("1loop: an AutoLink may not load %s" % spec.name)
            return tame(real(spec, *args))
        return create

    def executed(real):
        def run(module):
            result = real(module)
            tame(module)
            return result
        return run

    _imp.create_builtin, _imp.create_dynamic = guarded(_imp.create_builtin), guarded(_imp.create_dynamic)
    _imp.exec_builtin, _imp.exec_dynamic = executed(_imp.exec_builtin), executed(_imp.exec_dynamic)
    for loaded in starters:
        if loaded in sys.modules:
            tame(sys.modules[loaded])
    lookup = socket.getaddrinfo

    def getaddrinfo(host, *args, **kwargs):
        found = lookup(host, *args, **kwargs)
        for entry in found:
            resolved.add(str(entry[4][0]).lower())
        return found

    socket.getaddrinfo = getaddrinfo
    sys.addaudithook(hook)


sys.stdout = sys.stderr = _Printed()
for _path in (_job["lib"], _job["dir"], os.path.dirname(_job["boot"])):
    sys.path.insert(0, _path)
os.chdir(_job["data"])
_bridge = types.ModuleType("_autolink")
_bridge.call, _bridge.send, _bridge.job, _bridge.Error = _call, _send, _job, AutoLinkError
sys.modules["_autolink"] = _bridge
import autolink  # its parts, before the guard

with open(_job["main"], encoding="utf-8") as _file:
    _source = _file.read()
_guard(_job)
try:
    _code = compile(_source, "main.py", "exec")
    exec(_code, {"__name__": "__main__", "__file__": _job["main"], "__builtins__": builtins})
    _send({"end": True})
except SystemExit as stop:
    _send({"end": stop.code in (None, 0), "error": "" if stop.code in (None, 0) else "it stopped with %s" % (stop.code,)})
except BaseException as problem:
    frames = traceback.TracebackException.from_exception(problem)
    frames.stack = traceback.StackSummary.from_list([f for f in frames.stack if f.filename != _job["boot"]])
    _send({"end": False, "error": "".join(frames.format())[-6000:]})
finally:
    os._exit(0)
