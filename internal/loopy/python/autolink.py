# SPDX-License-Identifier: MPL-2.0
"""What an AutoLink's script imports: `import autolink`."""
import base64 as _base64
import json as _json
import os as _os
import time as _time

import _autolink as _bridge

Error = _bridge.Error
input = _bridge.job.get("input")  # what it was run with: any JSON value
test = bool(_bridge.job.get("test"))  # a trial: no message, no change
data = _bridge.job["data"]  # its own folder, kept between runs
out = _bridge.job["out"]  # this run's files


def output(value):
    """What the run gives back: text, or anything JSON holds. Several are kept in order."""
    _bridge.send({"output": value})


def file(path):
    """Gives back a file made under autolink.out or autolink.data (a picture is shown)."""
    path = _os.path.realpath(_os.path.join(out, path))
    _bridge.send({"file": path})


class Secret:
    """A vault entry's values: .user .password .totp .key ... ; never printed."""

    def __init__(self, values):
        self.__dict__["_values"] = values

    def __getattr__(self, name):
        try:
            return self.__dict__["_values"][name]
        except KeyError:
            raise AttributeError("this entry has no %r (it has: %s)" % (name, ", ".join(self._values))) from None

    def __getitem__(self, name):
        return self._values[name]

    def get(self, name, default=None):
        return self._values.get(name, default)

    def __repr__(self):
        return "Secret(…)"

    __str__ = __repr__


def secret(name):
    """The vault entry `name`, among those the user let this AutoLink use."""
    return Secret(_bridge.call("secret", name=name))


class _State(dict):
    """A dict kept between runs: read it, change it, it is saved when set or with save()."""

    def __init__(self):
        self._path = _os.path.join(data, "state.json")
        try:
            with open(self._path, encoding="utf-8") as f:
                super().__init__(_json.load(f))
        except (OSError, ValueError):
            super().__init__()

    def save(self):
        with open(self._path + ".tmp", "w", encoding="utf-8") as f:
            _json.dump(dict(self), f)
        _os.replace(self._path + ".tmp", self._path)

    def __setitem__(self, key, value):
        super().__setitem__(key, value)
        self.save()

    def __delitem__(self, key):
        super().__delitem__(key)
        self.save()


state = _State()


def new(key, items, id=lambda item: item.get("id") if isinstance(item, dict) else item, first=False):
    """The items not seen in earlier runs (kept under key). The first run sees nothing, unless first."""
    seen = state.get("seen:" + key)
    ids = [str(id(item)) for item in items]
    fresh = [] if seen is None and not first else [item for item, i in zip(items, ids) if i not in set(seen or [])]
    state["seen:" + key] = (ids + [s for s in (seen or []) if s not in set(ids)])[:2000]
    return fresh


def wake(text, key=""):
    """Wakes Loopy with what happened; the same key wakes it once."""
    return _bridge.call("wake", text=str(text), key=str(key))


def notify(title, text="", level="normal"):
    """A notification to the user: level "silent", "normal" or "urgent"."""
    return _bridge.call("notify", title=str(title), text=str(text), level=level)


def agent(project, title, brief, key=""):
    """Starts a coding agent in one of the user's projects (if the user let it); the same key starts it once."""
    return _bridge.call("agent", project=project, title=title, brief=brief, key=str(key))


class Response:
    def __init__(self, answer):
        self.status, self.headers, self.text = answer["status"], answer.get("headers") or {}, answer.get("body") or ""

    def json(self):
        return _json.loads(self.text)


def github(path, method="GET", body=None):
    """GitHub's API with the user's connector ("/repos/o/n/pulls"); reading, or writing if the user let it."""
    return Response(_bridge.call("github", path=path, method=method, body=body))


def sleep(seconds):
    _time.sleep(seconds)


class Page:
    """A page of Loopy's browser."""

    def __init__(self, id):
        self.id = id

    def _do(self, what, **args):
        return _bridge.call("page." + what, page=self.id, **args)

    def goto(self, url, wait="load"):
        """Opens an address; wait: "load", "idle" (no request for a moment) or ""."""
        self._do("goto", url=url, wait=wait)
        return self

    def reload(self):
        self._do("reload")
        return self

    def back(self):
        self._do("back")
        return self

    @property
    def url(self):
        return self._do("info")["url"]

    @property
    def title(self):
        return self._do("info")["title"]

    def wait(self, target=None, timeout=20, hidden=False, text=None, url=None):
        """Waits: seconds (a number), a CSS selector shown (or hidden), a text in the page, a part of the address."""
        if isinstance(target, (int, float)):
            _time.sleep(target)
            return self
        self._do("wait", selector=target or "", timeout=timeout, hidden=hidden, text=text or "", url=url or "")
        return self

    def wait_idle(self, timeout=20):
        """Waits until the page makes no request for half a second."""
        self._do("idle", timeout=timeout)
        return self

    def exists(self, selector):
        return bool(self.eval("!!document.querySelector(%s)" % _json.dumps(selector)))

    def click(self, selector=None, text=None, x=None, y=None, timeout=20):
        """Clicks a CSS selector, the element that says text, or a point; waits for it to show."""
        self._do("click", selector=selector or "", text=text or "", x=x or 0, y=y or 0, timeout=timeout)
        return self

    def hover(self, selector, timeout=20):
        self._do("hover", selector=selector, timeout=timeout)
        return self

    def fill(self, selector, value, timeout=20):
        """Puts a text in a field, in place of what it holds."""
        self._do("fill", selector=selector, value=str(value), timeout=timeout)
        return self

    def fill_secret(self, selector, entry, field="password", timeout=20):
        """Puts a vault value in a field, on the entry's own site; the script never holds it."""
        self._do("fill_secret", selector=selector, entry=entry, field=field, timeout=timeout)
        return self

    def type(self, text, selector=""):
        """Types on the keyboard, as a person."""
        self._do("type", selector=selector, text=text)
        return self

    def press(self, key):
        """Enter, Tab, Escape, ArrowDown, Ctrl+A…"""
        self._do("press", key=key)
        return self

    def select(self, selector, value):
        self._do("select", selector=selector, value=value)
        return self

    def check(self, selector, on=True):
        self._do("check", selector=selector, on=on)
        return self

    def scroll(self, dy=600, dx=0, selector=""):
        self._do("scroll", selector=selector, dx=dx, dy=dy)
        return self

    def text(self, selector="body"):
        """An element's text, None if it is not there."""
        return self.eval("(e => e ? (e.innerText ?? e.textContent).trim() : null)(document.querySelector(%s))" % _json.dumps(selector))

    def texts(self, selector):
        return self.eval("[...document.querySelectorAll(%s)].map(e => (e.innerText ?? e.textContent).trim())" % _json.dumps(selector))

    def attr(self, selector, name):
        return self.eval("(e => e ? e.getAttribute(%s) : null)(document.querySelector(%s))" % (_json.dumps(name), _json.dumps(selector)))

    def table(self, selector="table"):
        """A table's rows, as lists of texts."""
        return self.eval("[...(document.querySelector(%s)?.querySelectorAll('tr') ?? [])].map(r => [...r.children].map(c => c.innerText.trim()))" % _json.dumps(selector))

    def html(self, selector="html"):
        return self.eval("document.querySelector(%s)?.outerHTML ?? null" % _json.dumps(selector))

    def eval(self, script):
        """Runs JavaScript in the page; a promise is awaited, the value comes back."""
        return self._do("eval", script=script)

    def requests(self, match="", type=""):
        """The requests the page made: dicts with id, method, url, status, type."""
        return self._do("requests", match=match, type=type)

    def response(self, match, timeout=20):
        """Waits for a request whose address holds match; its body comes back as a Response."""
        return Response(self._do("response", match=match, timeout=timeout))

    def screenshot(self, name="page.png", full=False):
        """Takes a picture into autolink.out and gives it back."""
        answer = self._do("screenshot", full=full)
        path = _os.path.join(out, name)
        with open(path, "wb") as f:
            f.write(_base64.b64decode(answer["data"]))
        file(path)
        return path

    def close(self):
        self._do("close")

    def __enter__(self):
        return self

    def __exit__(self, *_):
        self.close()


class _Browser:
    def open(self, url="", wait="load", width=1280, height=800, mobile=False):
        """Opens a page in Loopy's browser (started if need be)."""
        page = Page(_bridge.call("page.open", width=width, height=height, mobile=mobile)["page"])
        if url:
            page.goto(url, wait)
        return page


browser = _Browser()
