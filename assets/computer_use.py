"""1loop's persistent XDG RemoteDesktop session and PipeWire frame reader.

Only this session's granted monitor and input devices are used. There is no
fallback to unrestricted capture or input when sharing is refused or revoked.
"""
import base64
import io
import json
import math
import os
import sys
import time
import uuid

import dbus
import dbus.mainloop.glib
import gi

gi.require_version("Gst", "1.0")
gi.require_version("GstVideo", "1.0")
gi.require_version("Gdk", "3.0")
from gi.repository import Gdk, GLib, Gst, GstVideo
from PIL import Image

GLib.set_prgname("1loop")
GLib.set_application_name("1loop")
dbus.mainloop.glib.DBusGMainLoop(set_as_default=True)
Gst.init(None)

SERVICE = "org.freedesktop.portal.Desktop"
PATH = "/org/freedesktop/portal/desktop"
REMOTE = "org.freedesktop.portal.RemoteDesktop"
SCREENCAST = "org.freedesktop.portal.ScreenCast"
SESSION = "org.freedesktop.portal.Session"
REQUEST = "org.freedesktop.portal.Request"


class DesktopSession:
    def __init__(self):
        self.bus = dbus.SessionBus()
        self.portal = self.bus.get_object(SERVICE, PATH)
        self.session = None
        self.pipeline = None
        self.stream = None
        self.closed_signal = None
        self.remote_fd = None
        self.last_sample = None
        self.pointer_uses_stream_pixels = False

    def request(self, interface, method, *arguments, **options):
        token = "one_loop_" + uuid.uuid4().hex
        sender = self.bus.get_unique_name()[1:].replace(".", "_")
        path = PATH + "/request/" + sender + "/" + token
        loop = GLib.MainLoop()
        response = []

        def received(code, results):
            response.extend([int(code), results])
            loop.quit()

        signal = self.bus.add_signal_receiver(
            received, signal_name="Response", dbus_interface=REQUEST, path=path
        )
        timeout = GLib.timeout_add_seconds(120, lambda: (loop.quit(), False)[1])
        try:
            options["handle_token"] = token
            actual_path = getattr(dbus.Interface(self.portal, interface), method)(
                *arguments, dbus.Dictionary(options, signature="sv")
            )
            if str(actual_path) != path:
                raise RuntimeError("Portal returned an unexpected request handle")
            if not response:
                loop.run()
            if not response or response[0] != 0:
                # Close the pending request so a timeout cannot grant access later.
                try:
                    dbus.Interface(self.bus.get_object(SERVICE, path), REQUEST).Close()
                except dbus.DBusException:
                    pass  # A cancelled request may already have been removed.
                raise RuntimeError("Desktop sharing was refused, cancelled or timed out")
            return response[1]
        finally:
            GLib.source_remove(timeout)
            signal.remove()

    def pump(self):
        context = GLib.MainContext.default()
        while context.pending():
            context.iteration(False)

    def close(self):
        session, self.session = self.session, None
        if self.pipeline is not None:
            self.pipeline.set_state(Gst.State.NULL)
            self.pipeline = None
        self.last_sample = None
        if self.remote_fd is not None:
            os.close(self.remote_fd)
            self.remote_fd = None
        if self.closed_signal is not None:
            self.closed_signal.remove()
            self.closed_signal = None
        if session:
            try:
                dbus.Interface(self.bus.get_object(SERVICE, session), SESSION).Close()
            except dbus.DBusException:
                pass

    def status(self):
        self.pump()
        if not self.session:
            raise RuntimeError("Desktop sharing is not active or was revoked. Ask the user before starting again.")
        # A signal may not have been dispatched while stdin was idle.
        dbus.Interface(self.bus.get_object(SERVICE, self.session),
                       "org.freedesktop.DBus.Peer").Ping()
        self.check_stream()
        return {"sharing": True}

    def check_stream(self):
        if self.pipeline is None:
            return
        message = self.pipeline.get_bus().pop_filtered(Gst.MessageType.ERROR | Gst.MessageType.EOS)
        if message is not None:
            reason = str(message.parse_error()[0]) if message.type == Gst.MessageType.ERROR else "Shared screen stream ended"
            self.close()
            raise RuntimeError(reason)

    def start(self, arguments):
        """Opens the sharing session. The permission is asked to persist: GNOME
        then returns a restore token, and a later start given that token
        resumes the same screen without its dialog. A token works once; each
        start returns the next one. Without a token, the dialog lets the user
        pick the screen."""
        if self.session:
            return self.status()
        try:
            try:
                devices = dbus.Interface(self.portal, "org.freedesktop.DBus.Properties").Get(REMOTE, "AvailableDeviceTypes")
            except dbus.DBusException as error:
                if error.get_dbus_name() in ("org.freedesktop.DBus.Error.UnknownInterface", "org.freedesktop.DBus.Error.UnknownMethod", "org.freedesktop.DBus.Error.InvalidArgs"):
                    raise RuntimeError("This desktop does not provide the XDG RemoteDesktop portal required for screen and keyboard/mouse control. X11 and capture-only portals are not supported by this integration.") from error
                raise
            if int(devices) & 3 != 3:
                raise RuntimeError("This desktop portal does not support both keyboard and pointer control")
            created = self.request(REMOTE, "CreateSession", session_handle_token="one_loop_" + uuid.uuid4().hex)
            self.session = str(created["session_handle"])
            self.closed_signal = self.bus.add_signal_receiver(
                lambda: self.close(), signal_name="Closed", dbus_interface=SESSION,
                path=self.session
            )
            # Request both devices together so typing never opens a second dialog.
            devices = {"types": dbus.UInt32(3), "persist_mode": dbus.UInt32(2)}
            if arguments.get("restore_token"):
                devices["restore_token"] = str(arguments["restore_token"])
            self.request(REMOTE, "SelectDevices", dbus.ObjectPath(self.session), **devices)
            self.request(SCREENCAST, "SelectSources", dbus.ObjectPath(self.session),
                         types=dbus.UInt32(1), multiple=dbus.Boolean(False), cursor_mode=dbus.UInt32(2))
            granted = self.request(REMOTE, "Start", dbus.ObjectPath(self.session), "")
            if int(granted.get("devices", 0)) & 3 != 3 or not granted.get("streams"):
                raise RuntimeError("Screen, keyboard and pointer access must all be granted")
            self.stream = granted["streams"][0]
            # Mutter expects stream pixels, while KDE expects compositor
            # coordinates. A missing logical_size alone cannot distinguish them.
            self.pointer_uses_stream_pixels = self.bus.name_has_owner("org.gnome.Mutter.RemoteDesktop")
            remote = dbus.Interface(self.portal, SCREENCAST).OpenPipeWireRemote(
                dbus.ObjectPath(self.session), dbus.Dictionary({}, signature="sv")
            )
            self.remote_fd = remote.take()
            self.pipeline = Gst.parse_launch(
                f"pipewiresrc fd={self.remote_fd} path={int(self.stream[0])} do-timestamp=true "
                "! videoconvert ! video/x-raw,format=RGB ! appsink name=frames max-buffers=1 drop=true sync=false"
            )
            self.pipeline.set_state(Gst.State.PLAYING)
            result = self.status()
            properties = self.stream[1]
            size = properties.get("size", (0, 0))
            position = properties.get("position", (0, 0))
            result["started"] = True
            result["screen"] = {"width": int(size[0]), "height": int(size[1]),
                                "x": int(position[0]), "y": int(position[1])}
            if granted.get("restore_token"):
                result["restore_token"] = str(granted["restore_token"])
            return result
        except Exception:
            self.close()
            raise

    def notify(self, method, *arguments):
        getattr(dbus.Interface(self.portal, REMOTE), method)(
            dbus.ObjectPath(self.session), dbus.Dictionary({}, signature="sv"), *arguments
        )

    def point(self, arguments, x_name="x", y_name="y"):
        x, y = float(arguments[x_name]), float(arguments[y_name])
        properties = self.stream[1]
        size = properties.get("logical_size", properties.get("size"))
        if not math.isfinite(x) or not math.isfinite(y) or x < 0 or y < 0:
            raise ValueError("Coordinates must be finite and inside the selected monitor")
        if size and (x >= size[0] or y >= size[1]):
            raise ValueError("Coordinates are outside the selected monitor")
        if "logical_size" not in properties and self.pointer_uses_stream_pixels:
            caps = self.pipeline.get_by_name("frames").get_static_pad("sink").get_current_caps()
            if caps is None:
                raise RuntimeError("Take a screenshot before coordinate input")
            video = GstVideo.VideoInfo.new_from_caps(caps)
            if size:
                x, y = x * video.width / size[0], y * video.height / size[1]
        self.notify("NotifyPointerMotionAbsolute", dbus.UInt32(self.stream[0]), dbus.Double(x), dbus.Double(y))

    def screenshot(self, arguments):
        sink = self.pipeline.get_by_name("frames")
        # PipeWire sends damage-driven frames. Keep the last frame for an idle
        # screen, but drain any newer samples before returning a capture.
        wait = 100 * Gst.MSECOND if self.last_sample is not None else 5 * Gst.SECOND
        sample = sink.emit("try-pull-sample", wait)
        while sample is not None:
            self.last_sample = sample
            sample = sink.emit("try-pull-sample", 0)
        self.check_stream()
        sample = self.last_sample
        if sample is None:
            raise RuntimeError("No frame received from the shared monitor")
        video = GstVideo.VideoInfo.new_from_caps(sample.get_caps())
        buffer = sample.get_buffer()
        ok, mapping = buffer.map(Gst.MapFlags.READ)
        if not ok:
            raise RuntimeError("Could not read the shared frame")
        try:
            image = Image.frombytes("RGB", (video.width, video.height), mapping.data, "raw", "RGB", video.stride[0])
        finally:
            buffer.unmap(mapping)
        max_width = max(320, min(4096, int(arguments.get("max_width", 1920))))
        max_height = max(320, min(4096, int(arguments.get("max_height", 1920))))
        image.thumbnail((max_width, max_height), Image.Resampling.LANCZOS)
        max_bytes = max(65536, min(4 << 20, int(arguments.get("max_bytes", 2 << 20))))
        while True:
            output = io.BytesIO()
            image.save(output, format="PNG")
            if output.tell() <= max_bytes:
                break
            image.thumbnail((max(1, int(image.width*.8)), max(1, int(image.height*.8))), Image.Resampling.LANCZOS)
        properties = self.stream[1]
        logical = properties.get("logical_size", properties.get("size", (video.width, video.height)))
        origin = properties.get("position", (0, 0))
        metadata = {"source":"xdg-remote-desktop-pipewire", "width":image.width, "height":image.height,
                    "coordinate_width":int(logical[0]), "coordinate_height":int(logical[1]),
                    "origin_x":int(origin[0]), "origin_y":int(origin[1]),
                    "scale_x":image.width / logical[0], "scale_y":image.height / logical[1]}
        return [{"type":"text", "text":json.dumps(metadata)},
                {"type":"image", "mimeType":"image/png", "data":base64.b64encode(output.getvalue()).decode()}]

    def action(self, name, arguments):
        if name == "start":
            return self.start(arguments)
        fields = {
            "status": (), "screenshot": ("max_width", "max_height", "max_bytes"),
            "click": ("x", "y", "button", "click_count"), "move": ("x", "y"),
            "scroll": ("x", "y", "direction", "pages"),
            "drag": ("start_x", "start_y", "end_x", "end_y"),
            "type_text": ("text",), "press_key": ("key",),
        }
        if name not in fields:
            raise ValueError("Unknown portal action: " + name)
        unknown = set(arguments) - set(fields[name])
        if unknown:
            raise ValueError("Unsupported arguments for " + name + ": " + ", ".join(sorted(unknown)) +
                             ". Coordinate actions use x/y on the shared monitor; use perform_action for accessibility selectors.")
        self.status()
        if name == "status":
            return {"sharing":True}
        if name == "screenshot":
            return self.screenshot(arguments)
        if name in ("click", "move"):
            buttons = {"left":0x110, "right":0x111, "middle":0x112}
            button = buttons[arguments.get("button", "left")]
            count = max(1, min(3, int(arguments.get("click_count", 1))))
            self.point(arguments)
            if name == "click":
                for _ in range(count):
                    self.notify("NotifyPointerButton", dbus.Int32(button), dbus.UInt32(1))
                    time.sleep(.035)
                    self.notify("NotifyPointerButton", dbus.Int32(button), dbus.UInt32(0))
        elif name == "scroll":
            direction = arguments["direction"]
            axis, sign = {"up":(0,-1), "down":(0,1), "left":(1,-1), "right":(1,1)}[direction]
            steps = max(1, min(30, round(float(arguments.get("pages", 1))*5)))
            if "x" in arguments or "y" in arguments:
                self.point(arguments)
            self.notify("NotifyPointerAxisDiscrete", dbus.UInt32(axis), dbus.Int32(sign*steps))
        elif name == "drag":
            self.point(arguments, "start_x", "start_y")
            # Validate the destination before pressing a button.
            destination = {"x":arguments["end_x"], "y":arguments["end_y"]}
            self.point(destination)
            self.point(arguments, "start_x", "start_y")
            self.notify("NotifyPointerButton", dbus.Int32(0x110), dbus.UInt32(1))
            try:
                time.sleep(.035)
                self.point(destination)
            finally:
                self.notify("NotifyPointerButton", dbus.Int32(0x110), dbus.UInt32(0))
        elif name == "type_text":
            for character in arguments["text"]:
                key = {"\n":Gdk.KEY_Return, "\t":Gdk.KEY_Tab}.get(character, Gdk.unicode_to_keyval(ord(character)))
                self.notify("NotifyKeyboardKeysym", dbus.Int32(key), dbus.UInt32(1))
                self.notify("NotifyKeyboardKeysym", dbus.Int32(key), dbus.UInt32(0))
        elif name == "press_key":
            aliases = {"ctrl":"Control_L", "control":"Control_L", "alt":"Alt_L", "shift":"Shift_L",
                       "super":"Super_L", "meta":"Super_L", "enter":"Return", "escape":"Escape",
                       "esc":"Escape", "tab":"Tab", "space":"space", "backspace":"BackSpace",
                       "delete":"Delete", "home":"Home", "end":"End", "pageup":"Page_Up",
                       "pagedown":"Page_Down", "arrowleft":"Left", "arrowright":"Right",
                       "arrowup":"Up", "arrowdown":"Down"}
            keys = []
            modifiers = {Gdk.KEY_Control_L:29, Gdk.KEY_Alt_L:56,
                         Gdk.KEY_Shift_L:42, Gdk.KEY_Super_L:125}
            for token in arguments["key"].split("+"):
                token = token.strip()
                key = Gdk.keyval_from_name(aliases.get(token.lower(), token.lower() if len(token)==1 else token.title()))
                if not key or key == Gdk.KEY_VoidSymbol:
                    raise ValueError("Unsupported key: " + token)
                # Hold modifiers as physical keys; let the compositor resolve
                # the other symbols in its active layout, not the first GDK group.
                keys.append(("NotifyKeyboardKeycode", modifiers[key]) if key in modifiers
                            else ("NotifyKeyboardKeysym", key))
            pressed = []
            try:
                for method, key in keys:
                    self.notify(method, dbus.Int32(key), dbus.UInt32(1))
                    pressed.append((method, key))
                time.sleep(.035)
            finally:
                for method, key in reversed(pressed):
                    self.notify(method, dbus.Int32(key), dbus.UInt32(0))
        return {"ok":True, "action":name}


desktop = DesktopSession()
try:
    for line in sys.stdin:
        request = json.loads(line)
        if "id" not in request:
            continue
        try:
            if request["method"] == "initialize":
                result = {"protocolVersion":"2025-03-26", "capabilities":{"tools":{}},
                          "serverInfo":{"name":"1loop-desktop-portal", "version":"1.0"}}
            elif request["method"] == "tools/call":
                parameters = request["params"]
                value = desktop.action(parameters["name"], parameters.get("arguments") or {})
                content = value if isinstance(value, list) else [{"type":"text", "text":json.dumps(value)}]
                result = {"content":content, "isError":False}
            else:
                raise ValueError("Unknown method")
            response = {"jsonrpc":"2.0", "id":request["id"], "result":result}
        except Exception as error:
            response = {"jsonrpc":"2.0", "id":request["id"],
                        "error":{"code":-32000, "message":str(error)}}
        print(json.dumps(response), flush=True)
finally:
    desktop.close()
