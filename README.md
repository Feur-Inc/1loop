# 1loop

Application de bureau en Go avec une fenêtre native Fyne, une interface sombre, des conversations et un sélecteur de modèles. Le moteur et l’interface sont écrits en Go ; l’interface ne nécessite aucun navigateur. La connexion OAuth utilise le navigateur et un callback HTTP temporaire sur `localhost:1455`, écouté uniquement sur 127.0.0.1.

## Windows

Windows 10 version 1809 or newer (64-bit) is required. Download the Windows archive from a release, extract it and run `1loop.exe`. The desktop uses PowerShell and native ConPTY terminals; Git is required for workspace diffs. Browser Use detects installed Chrome, Chromium or Microsoft Edge. Computer Use uses built-in Windows PowerShell/.NET, UI Automation and Win32 input/capture APIs, without Python or the Linux Codex helper.

If the desktop stays blank because the graphics driver does not provide OpenGL, run `1loop-software.cmd` instead. This uses the bundled portable Mesa CPU renderer without changing system drivers. It needs the Microsoft Visual C++ 2015–2022 x64 runtime and can be slower than hardware rendering. The renderer is packaged from [mesa-dist-win](https://github.com/pal1000/mesa-dist-win); its licenses are included in `software/licenses`.

The phone-sharing feature can trigger a Windows firewall prompt. Desktop-only use does not require allowing inbound network access; disable phone sharing in Settings when unused. Computer Use consent is separate from the firewall prompt.

The first Computer Use request opens a 1loop consent dialog to select a monitor or the whole desktop. Remembering permission reuses that choice; Settings → General → Computer Use can change or forget it. Coordinates and screenshots use physical pixels, including display scaling and negative monitor origins. The Windows session must be unlocked and interactive. UAC secure desktops and applications with higher integrity remain protected; 1loop does not bypass these restrictions.

Build with Go 1.23+ and MinGW-w64 GCC on PATH:

```powershell
go build -trimpath -ldflags="-s -w -H=windowsgui -extldflags=-static" -o bin/1loop.exe ./cmd/1loop
./bin/1loop.exe -workspace C:/Projects/my-project
```

The Windows deliverable is the desktop application. It can connect to the existing remote server. Application state follows the Windows user configuration directory, and logs/helpers follow its cache directory.

## Lancer sous Linux

Go 1.23+, un compilateur C, les bibliothèques de développement OpenGL, X11 et Wayland (`libwayland-dev`, `libxkbcommon-dev`), Bash et `diff` sont nécessaires. La première compilation télécharge Fyne et ses dépendances.

```bash
make            # compile bin/1loop (X11) et bin/1loop-wayland (Wayland natif)
make run        # compile puis ouvre 1loop sur le dossier courant
make dev        # pareil, avec le journal développeur (option -dev)
bash ci.sh      # vérifications locales Go/Linux/Android, tests, builds et lint
make release    # versions finales dans dist/ : archive Linux (bureau X11 + Wayland, serveur) et APK release
./bin/1loop -workspace /chemin/vers/projet
```

`ci.sh` remplace GitHub Actions. Il nécessite les outils Go/Linux ci-dessus,
un JDK 21 et le SDK Android (`platforms;android-37.0`, `build-tools;37.0.0` ;
NDK `30.0.16248370` pour reconstruire les composants natifs). Il n'installe
aucun paquet système et s'arrête à la première vérification échouée. Les
builds restent sur la machine. La version Windows (`bin/windows/` : bureau,
serveur et rendu logiciel Mesa) est compilée dans Docker avec MinGW ; ses tests
Go se lancent sur Windows lui-même.

Lancez toujours `bin/1loop` : dans une session Wayland il passe la main à `bin/1loop-wayland` (texte net, sans mise à l’échelle XWayland), sinon il reste en X11. `ONELOOP_BACKEND=x11` force X11 ; `ONELOOP_SYSTEM_TITLEBAR=1` remet la barre de titre du système.

**Journal développeur** : `make dev` (ou `./bin/1loop -dev`, ou `ONELOOP_DEV=1`) écrit dans le terminal et dans `~/.cache/1loop/dev.log` (la session précédente reste dans `dev.log.1`) tout ce que fait 1loop : actions de l’utilisateur, événements de chaque agent, lignes du processus Claude Code, outils, sauvegardes, et les performances de l’interface (images dessinées, tours trop longs, opérations lentes). Un chien de garde suit le fil de l’interface : s’il s’arrête plus de 1,5 s, la pile de toutes les goroutines est écrite, ce qui dit où il est bloqué. `kill -USR1 <pid>` écrit la même chose à la demande.

**Fenêtre masquée** : sous Wayland, 1loop cadence lui-même ses images sur l’écran au lieu de laisser le pilote graphique attendre dans l’échange d’images. Cette attente ne finissait jamais pour une fenêtre masquée (recouverte, réduite, sur un autre espace de travail) : l’interface se bloquait, l’agent et la télécommande avec elle. `ONELOOP_VSYNC=1` rétablit l’ancien comportement, pour comparer.

Au démarrage, 1loop s’enregistre auprès du bureau dans le dossier de l’utilisateur (`~/.local/share/applications/1loop.desktop` et son icône) : le dock, la vue d’ensemble et le menu des applications affichent ainsi son nom et son icône. L’entrée pointe vers le binaire lancé et n’est réécrite que s’il a changé de place.

## Connexions

Le panneau **Settings → Connections** permet d’ajouter, désactiver ou retirer autant de connexions indépendantes que nécessaire, y compris plusieurs comptes du même service. Leurs modèles sont réunis dans le sélecteur du compositeur. **Load / refresh models** charge le catalogue ; les cases choisissent les modèles visibles. **Configure** règle les paramètres de chaque modèle ; enregistrer ensuite la connexion pour les appliquer.

- **vLLM**, sélectionné par défaut : `http://localhost:8000/v1`. Les modèles sont découverts via `/models`. Le serveur doit prendre en charge le streaming Chat Completions et le tool calling automatique pour utiliser les outils.
- **OpenRouter** : renseigner une clé dans Connexions, ou démarrer avec la variable `OPENROUTER_API_KEY`. Choisir un modèle qui prend en charge les outils.
- **Compte ChatGPT** : choisir « Continue with ChatGPT », ouvrir le lien et autoriser la connexion. OAuth avec PKCE et renouvellement des jetons, selon le flux utilisé par OpenCode. L’inférence et le catalogue passent par le backend d’abonnement `https://chatgpt.com/backend-api/codex`, avec l’identifiant de compte. Aucun processus Codex CLI ni chat dans l’application Codex n’est créé. Les anciens comptes du flux de partage direct nécessitent une nouvelle connexion. Le port local 1455 doit être disponible.

- **API compatibles OpenAI** : URL `/v1` personnalisée et clé facultative, avec Chat Completions et streaming.

- **Claude Code** : 1loop pilote le Claude Code installé sur l’ordinateur, sans le modifier, dans son mode programmatique documenté (`claude -p` en `stream-json`). Ici le travail tourne dans le harness de Claude Code, pas dans celui de 1loop : sa connexion (l’abonnement Claude), ses outils, ses règles de permission, `CLAUDE.md` et ses serveurs MCP. 1loop envoie les messages, affiche le flux (texte, réflexion, appels d’outils, diffs) et relaie ses demandes de permission, sur l’ordinateur comme sur le téléphone. Les modèles et niveaux d’effort viennent de Claude Code lui-même ; chaque conversation garde sa session et la reprend. Le sélecteur les marque « · Claude Code ». Garde-fou : si un tour n’est pas sur la connexion de l’abonnement, ou commence à puiser dans l’« extra usage », 1loop l’arrête aussitôt. La connexion est proposée automatiquement au premier lancement si `claude` est installé.

  Quand Claude Code laisse une tâche tourner en arrière-plan (commande lancée en arrière-plan, Monitor), le tour reste ouvert : la ligne d’activité indique la tâche attendue et le temps écoulé, et Claude Code reprend seul quand elle se termine. **Stop** arrête Claude Code et tout ce qu’il a lancé. Les sous-agents de Claude Code sont désactivés pour l’instant (`--disallowedTools`, voir `claudeDisabledTools` dans `internal/agent/claudecode.go`) : l’interface ne sait pas encore montrer leurs étapes.

Pour OpenRouter, **Fetch providers** propose les fournisseurs du modèle. L’ordre, les fallbacks et les autres options de routage sont modifiables en JSON, globalement ou par modèle. Les paramètres de requête (température, limites de tokens, etc.) suivent le même principe ; les réglages du modèle remplacent les valeurs correspondantes de la connexion.

Cliquer sur le modèle dans le compositeur ouvre le réglage d’effort lorsqu’il est disponible ; cliquer sur son nom dans ce panneau ouvre la liste des modèles. Les niveaux proviennent du catalogue du fournisseur ou, pour vLLM, du template du tokenizer du serveur. Si ce dernier n’est pas publié, 1loop consulte le template public du dépôt indiqué par le serveur ; sa source figure dans les paramètres du modèle. Un template modifié côté serveur peut donc exposer des capacités différentes. Sans métadonnées connues, aucun réglage d’effort n’est affiché.

Les réglages s’ouvrent en pleine fenêtre : leurs sections remplacent la barre latérale, la section choisie remplace la conversation, et une connexion ou un modèle s’ouvre comme une page à la place de la précédente (Échap ou le lien en haut pour revenir). La plupart des réglages s’appliquent dès qu’ils changent ; une connexion s’édite comme un brouillon, enregistré avec les boutons fixés en bas de sa page.

**Settings → General** règle le workspace et les permissions. Les dossiers et images sont choisis avec le sélecteur du bureau (portail XDG, sinon zenity ou kdialog). **Instructions** ajoute des consignes utilisateur au prompt système existant, pour les prochaines requêtes de tous les chats.

## Réponses

Les réponses sont mises en page par 1loop lui-même (`internal/ui/markdown.go`, `mdinline.go`, `flowtext.go`) : titres, paragraphes, listes à puces, numérotées et imbriquées, cases à cocher, citations, filets, tableaux, blocs de code avec leur bouton de copie, gras, italique, barré, code en ligne sur fond teinté, liens `[texte](adresse)` et adresses nues, cliquables (ouverts par l’application du bureau).

**Formules** : `$…$`, `\(…\)`, `$$…$$` et `\[…\]` sont composées par le TeX de l’ordinateur quand `latex` et `dvipng` sont installés (paquets `texlive-latex-extra` et `dvipng` sous Debian), avec `amsmath` et `amssymb`. Elles sont calculées à côté de l’interface puis gardées dans `~/.cache/1loop/math`. Sans TeX, ou si une formule ne compile pas, sa source reste affichée comme du code. TeX tourne sans accès au shell et sans lecture de fichiers hors de son dossier de travail.

**Sélection** : le texte d’une réponse se sélectionne à la souris, d’un paragraphe à l’autre, code et tableaux compris ; double-clic pour un mot, Ctrl+C pour copier, Échap pour annuler.

## Outils

Les connexions utilisent quatre outils exécutés par le backend Go :

- `read_file` : lecture d’un fichier texte, jusqu’à 1 MiB.
- `list_directory` : liste des fichiers d’un dossier.
- `shell` : commande Bash, avec aperçu et validation, et sortie limitée à 128 KiB. Elle s’arrête après 60 secondes, ou après `timeout_ms` choisi par le modèle (10 minutes au plus). Avec `run_in_background`, la commande démarre et l’outil répond aussitôt, sans limite de durée (serveur, build long, surveillance) : sa sortie est gardée, le modèle la suit avec `background_task` (liste, sortie, arrêt) et il est réveillé quand elle se termine (nouveau tour, ou message ajouté au tour en cours). Ces tâches apparaissent dans l’onglet **Tasks** du panneau latéral, sous la liste, chacune avec son bouton **Stop**, distinct du Stop du chat ; elles s’arrêtent quand le chat est supprimé ou que 1loop se ferme, et ne survivent pas à un redémarrage. Bureau, serveur et téléphone.
- `edit_file` : remplacement exact unique ou création d’un fichier, avec diff unifié avant validation. Les suppressions passent par `shell` (`rm`, `mv`…), limitées au workspace par le prompt, avec validation hors mode YOLO.

Le menu **Approval required** du compositeur demande une validation avant chaque commande ou édition. **YOLO mode** les approuve automatiquement, sans bac à sable ; ce réglage est également accessible dans Settings. **Auto mode** (compte ChatGPT ; Claude Code utilise son propre mode « auto ») suit la méthode de Codex :
- les éditions dans le workspace (hors `.git`) et les commandes qui ne font que lire (liste de Codex : `ls`, `cat`, `grep`, `sed -n 5p`, `git status`…, analysées avec un vrai parseur bash) passent directement ;
- les commandes destructrices ou sortantes (`rm`, `rmdir`, `truncate`, `dd`, `git reset`/`clean`/`rm`/`restore`/`push`, `checkout --`, `find -delete`, `chmod -R`, `sudo`, `kill`, `curl … | sh`…, y compris dans `bash -c`, `xargs`, `find -exec` ou `$(…)`) passent toujours par le relecteur Guardian ;
- le reste s'exécute sans revue dans un **bac à sable** : Landlock sous Linux (noyau 5.13+, appliqué par le noyau, environ 17 ms par commande), abwrap sur le téléphone. La commande lit partout, garde le réseau, mais n'écrit que dans le workspace, les dossiers temporaires et les caches de compilation (`~/.cache`, `~/go`, `~/.npm`, `~/.cargo`…). Pour écrire ailleurs, le modèle relance la commande avec `escalate: true` et une justification, qui passe par le relecteur ;
- sans bac à sable (Windows, vieux noyau), seules les lectures et les créations simples dans le workspace (`mkdir`, `touch`, `cp`/`mv` sans écraser) passent sans revue ; le reste va au relecteur.

Le relecteur (le modèle `codex-auto-review` si le compte l'a, sinon celui du chat avec un raisonnement faible) approuve ou soumet l'action à l'utilisateur avec sa raison ; chaque approbation laisse une note dans le chat. Phone Use et le shell Android passent eux aussi par le relecteur à chaque action en mode Auto ; en mode Approval, ADB est coupé et grisé (chaque tap attendrait l'utilisateur). Limite connue : un script ou un interpréteur (`python script.py`) lancé dans le bac à sable peut modifier ou supprimer des fichiers du workspace sans revue, jamais en dehors.

Les lectures et éditions sont limitées au workspace, y compris la résolution des liens symboliques. Le shell s’exécute avec les droits de l’utilisateur : **la validation d’une commande lui permet d’agir sur la machine**, sans sandbox supplémentaire.

Les conversations, projets et réglages sont conservés dans `~/.config/1loop/state.json`, hors du projet (un ancien `.1loop/state.json` est migré automatiquement). Les clés saisies restent en mémoire et doivent être renseignées à nouveau après redémarrage. Les jetons OAuth sont enregistrés dans `~/.config/1loop/openai-<profil>.json` (ou `openai.json` pour le compte historique) avec des permissions 0600 et renouvelés par 1loop. Aucune conversation Codex n’est créée. Les anciens historiques locaux sont conservés ; une nouvelle autorisation OAuth est nécessaire pour OpenAI.

## Computer Use

The `computer_use` tool controls the real Linux desktop. It first returns its usage instructions, then `start` opens the native XDG RemoteDesktop permission dialog for screen sharing, keyboard and pointer control. One live portal session is reused between calls, prompts and chats until sharing is stopped, revoked, or 1loop exits. `stop` ends sharing; the desktop's sharing indicator can also revoke access. No unrestricted capture or input fallback is used after refusal or revocation.

When "Remember this choice" is ticked in that dialog, 1loop keeps the portal's restore token in `~/.config/1loop/computer-use.json` (never shown to the model): later sessions, after a restart too, resume the same screen without the dialog. **Settings → General → Computer Use** shows the shared screen; **Change screen…** opens the dialog again to pick another, **Forget** stops sharing and revokes the permission.

Screenshots come from the PipeWire stream granted by the desktop, which may cover a monitor or the workspace. The model receives the image and its logical coordinate dimensions, origin and image scale. Coordinate actions support click, move, scroll and drag; keyboard actions support literal text and named key combinations. Desktop operations are serialized because all conversations share the same physical keyboard and pointer.

Runtime requirements: `/usr/bin/python3`, PyGObject with GTK/Gdk 3 and GStreamer/GstVideo introspection, `python3-dbus`, Pillow, GStreamer's `pipewiresrc` plugin, and a desktop portal implementing RemoteDesktop and ScreenCast. On Debian/Ubuntu the relevant packages include `python3-gi`, `python3-dbus`, `python3-pil`, `gir1.2-gtk-3.0`, `gir1.2-gstreamer-1.0`, `gir1.2-gst-plugins-base-1.0` and `gstreamer1.0-pipewire`.

Linux desktop support depends on the portal backend, not just whether 1loop's window uses X11 or Wayland. A capture-only portal is insufficient. This integration has no native X11 capture/input fallback: desktops without RemoteDesktop report that control is unavailable. XWayland application windows can be controlled inside a Wayland session when the desktop provides the required portal. Coordinate conversion accounts for Mutter's stream-pixel input and portals such as KDE that use compositor coordinates.

AT-SPI accessibility and compositor window operations reuse the `codex-computer-use-linux` MCP backend examined in `~/codex-desktop-linux/computer-use-linux`. Set `ONELOOP_COMPUTER_BACKEND` to its executable, place it next to 1loop's binary, or install it on `PATH`. The Codex Desktop installation under `/opt/codex-desktop` is also detected. Coordinate input and shared-screen capture work without that optional backend. `describe` returns the backend's accessibility/window schemas. Extensions are never installed automatically.

Claude Code receives the same tool through 1loop's existing MCP bridge. Screen-sharing permission remains a native desktop decision, including in YOLO mode; it cannot be granted by the model. A desktop that does not support persistent grants may request permission again after restarting 1loop.

## Télécommande Android

`android/` contient l'application Android native (Kotlin + Jetpack Compose) qui pilote 1loop depuis un téléphone : même style, même logo, même animation de lancement et même roue de travail que l'application de bureau.

- 1loop sert ses conversations sur le réseau local : découverte en broadcast UDP (port 47100), API JSON et événements en direct en HTTPS (port 47101), avec un certificat propre à l'ordinateur (`~/.config/1loop/remote/`) que le téléphone épingle par son empreinte SHA-256. Désactivable dans **Settings → Phone remote**, où l'on retire aussi les téléphones appairés : « Forget » coupe aussitôt leurs connexions.
- Appairage : **Settings → Phone remote → Pair a phone** affiche un QR code (adresse, empreinte du certificat, secret à usage unique de 128 bits) à scanner dans l'app (**Scan QR code**). Sans caméra, toucher l'ordinateur dans la liste : il affiche un code à six chiffres à saisir sur le téléphone, et les deux écrans montrent le début de l'empreinte à comparer. Cinq essais par code ; après vingt mauvais codes au total, l'appairage par code s'arrête jusqu'à ce que l'utilisateur ouvre le QR code sur l'ordinateur. Seule l'empreinte du jeton est conservée ; il ne circule qu'en HTTPS, et le téléphone ne le confie qu'à un ordinateur présentant le même certificat. Les téléphones appairés avant le passage en HTTPS doivent être appairés à nouveau.
- Ce que le téléphone envoie passe par les mêmes chemins que la fenêtre : la conversation s'ouvre et s'affiche sur l'ordinateur. Les approbations peuvent se donner sur l'un ou l'autre.
- Les captures que les outils montrent au modèle (navigateur, Computer Use, Phone Use) et les images jointes aux messages s'affichent en miniature dans la conversation, sur le téléphone comme sur le bureau connecté à un serveur ; un toucher ou un clic les agrandit (`GET /api/chats/{id}/items/{index}/images/{n}`).
- Le trafic circule en HTTP sur le réseau local : à réserver à un réseau de confiance.

```bash
cd android && ./gradlew assembleRelease   # JDK 17+, SDK Android (compileSdk 37)
adb install -r app/build/outputs/apk/release/app-release.apk
```

## Raccourcis

- Entrée envoie le message ; pendant qu’une tâche tourne, il est mis en file d’attente et devient le tour suivant une fois la réponse terminée.
- Ctrl + Entrée, pendant une tâche : le message est injecté dans la tâche en cours, à la prochaine étape (après les appels d’outils en cours).
- Maj + Entrée ajoute une ligne ; le bouton carré arrête la réponse. Les messages en attente restent dans la file, réordonnables et modifiables ; Entrée sur un champ vide relance la file.
- Les tâches tournent en arrière-plan : on peut ouvrir une autre conversation, en démarrer une nouvelle ou aller dans Settings pendant que l’agent travaille (un point ambre marque les conversations en cours). Les demandes d’approbation s’affichent où que l’on soit.
- Le crayon d’un message en attente le remet dans le champ de saisie : Entrée l’enregistre à sa place, Échap annule.
- Clic sur un appel d’outil : panneau latéral à onglets avec la commande ou le diff, la sortie complète et la durée d’exécution.
- Le panneau latéral appartient à la conversation : chacune garde le sien (ouvert ou replié, sa largeur, ses onglets et celui qui est affiché) et le retrouve tel quel quand on y revient, y compris après un redémarrage (un terminal repart alors dans un nouveau shell). Les terminaux d’une conversation continuent de tourner pendant qu’on en regarde une autre.
- Liste de tâches (onglet **Tasks**) : chaque tâche porte l’anneau de 1loop, gris tant qu’elle attend, en rotation pendant que l’agent y travaille, refermé autour d’une coche une fois faite. La première fois que l’agent y inscrit des tâches, le panneau s’ouvre sur la liste.
- Ctrl + K : nouvelle conversation. Ctrl + B : replier ou déplier la barre latérale.
- Ctrl + Maj + R : relance complète de l’application (interface et moteur).
- Clic droit ou « ⋯ » sur une conversation ou un projet : renommer, supprimer, retirer.

Sources des intégrations : [implémentation OAuth OpenCode](https://github.com/anomalyco/opencode/blob/dev/packages/opencode/src/plugin/openai/codex.ts), [vLLM](https://docs.vllm.ai/en/stable/serving/openai_compatible_server/), [OpenRouter](https://openrouter.ai/docs/guides/features/tool-calling).

## Headless server

Build `bin/1loop-server` with `make server` to run the 1loop engine on a Debian VM without a desktop. Desktop and Android clients connect over HTTPS with console-approved pairing. Browser Use and persistent terminal tabs run on the server; Computer Use is excluded. See [docs/SERVER.md](docs/SERVER.md) for setup, certificate verification, credentials and remote tools.
