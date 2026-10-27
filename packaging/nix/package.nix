# 1loop for Nix and NixOS. Built from the repository by the flake
# (flake.nix): nix build, nix run, or the package in a NixOS configuration.
{
  lib,
  buildGoModule,
  makeDesktopItem,
  copyDesktopItems,
  makeWrapper,
  pkg-config,
  libGL,
  libx11,
  libxrandr,
  libxxf86vm,
  libxi,
  libxcursor,
  libxinerama,
  libxext,
  wayland,
  libxkbcommon,
  python3,
  gobject-introspection,
  gtk3,
  pango,
  gdk-pixbuf,
  harfbuzz,
  at-spi2-core,
  gst_all_1,
  pipewire,
  version,
}:

let
  # Computer Use's portal helper (assets/computer_use.py) and what it
  # imports, kept to that one process: 1loop's own environment, which the
  # terminal and the agent's commands inherit, stays untouched.
  computerPython = python3.withPackages (p: [
    p.pygobject3
    p.dbus-python
    p.pillow
  ]);
  typelibs = [
    gtk3
    pango
    gdk-pixbuf
    harfbuzz
    at-spi2-core
    gst_all_1.gstreamer
    gst_all_1.gst-plugins-base
    gobject-introspection
  ];
in
buildGoModule {
  pname = "1loop";
  inherit version;

  src = lib.fileset.toSource {
    root = ../..;
    fileset = lib.fileset.unions [
      ../../go.mod
      ../../go.sum
      ../../VERSION
      ../../cmd
      ../../internal
      ../../assets
      ../../third_party
    ];
  };
  vendorHash = "sha256-b4PRHYNp4oaGOCoUbuGPPeoRUmUu87AXIq/s+JZO53o=";

  nativeBuildInputs = [
    pkg-config
    copyDesktopItems
    makeWrapper
  ];
  buildInputs = [
    libGL
    libx11
    libxrandr
    libxxf86vm
    libxi
    libxcursor
    libxinerama
    libxext
    wayland
    libxkbcommon
  ];

  # Two builds, as in the Makefile: GLFW picks X11 or Wayland when it is
  # compiled, and 1loop starts 1loop-wayland beside it in a Wayland session.
  buildPhase = ''
    runHook preBuild
    flags="-s -w -X 1loop/internal/update.Version=${version} -X 1loop/internal/update.Packaged=Nix"
    go build -trimpath -ldflags="$flags" -o build/1loop ./cmd/1loop
    CGO_CFLAGS="$CGO_CFLAGS -w" go build -trimpath -ldflags="$flags" -tags wayland -o build/1loop-wayland ./cmd/1loop
    go build -o build/1loop-release ./cmd/1loop-release
    runHook postBuild
  '';

  # The tests need a display and a sandbox-free shell: the CI runs them.
  doCheck = false;

  installPhase = ''
    runHook preInstall
    install -Dm755 build/1loop build/1loop-wayland -t $out/libexec/1loop
    makeWrapper ${computerPython}/bin/python3 $out/libexec/1loop/computer-python \
      --prefix GI_TYPELIB_PATH : ${lib.makeSearchPathOutput "out" "lib/girepository-1.0" typelibs} \
      --prefix GST_PLUGIN_SYSTEM_PATH_1_0 : ${
        lib.makeSearchPathOutput "out" "lib/gstreamer-1.0" [
          gst_all_1.gst-plugins-base
          gst_all_1.gst-plugins-good
          pipewire
        ]
      }
    makeWrapper $out/libexec/1loop/1loop $out/bin/1loop \
      --set-default ONELOOP_COMPUTER_PYTHON $out/libexec/1loop/computer-python
    build/1loop-release icons $out/share/icons
    runHook postInstall
  '';

  desktopItems = [
    (makeDesktopItem {
      name = "1loop";
      desktopName = "1loop";
      genericName = "Coding agent";
      comment = "Your project. Your model. One loop.";
      exec = "1loop";
      icon = "1loop";
      categories = [ "Development" ];
      keywords = [
        "AI"
        "agent"
        "LLM"
        "code"
        "assistant"
      ];
      startupWMClass = "1loop";
      startupNotify = true;
    })
  ];

  meta = {
    description = "Coding agent for your project, with your model";
    homepage = "https://github.com/Feur-Inc/1loop";
    platforms = [ "x86_64-linux" ];
    mainProgram = "1loop";
  };
}
