# 1loop

Application de bureau en Go avec une fenêtre native Fyne, une interface sombre, des conversations et un sélecteur de modèles. Le moteur et l’interface sont écrits en Go ; l’interface ne nécessite aucun navigateur. La connexion OAuth utilise le navigateur et un callback HTTP temporaire sur `localhost:1455`, écouté uniquement sur 127.0.0.1.

## Lancer sous Linux

Go 1.23+, un compilateur C, les bibliothèques de développement OpenGL, X11 et Wayland (`libwayland-dev`, `libxkbcommon-dev`), Bash et `diff` sont nécessaires. La première compilation télécharge Fyne et ses dépendances.

```bash
make            # compile bin/1loop (X11) et bin/1loop-wayland (Wayland natif)
make run        # compile puis ouvre 1loop sur le dossier courant
make dev        # pareil, avec le journal développeur (option -dev)
./bin/1loop -workspace /chemin/vers/projet
```

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

  Quand Claude Code laisse une tâche tourner en arrière-plan (commande lancée en arrière-plan, Monitor), le tour reste ouvert : la ligne d’activité indique la tâche attendue et le temps écoulé, et Claude Code reprend seul quand elle se termine. **Stop** arrête Claude Code et tout ce qu’il a lancé. Les sous-agents de Claude Code sont désactivés pour l’instant (`--disallowedTools`, voir `claudeDisabledTools` dans `claudecode.go`) : l’interface ne sait pas encore montrer leurs étapes.

Pour OpenRouter, **Fetch providers** propose les fournisseurs du modèle. L’ordre, les fallbacks et les autres options de routage sont modifiables en JSON, globalement ou par modèle. Les paramètres de requête (température, limites de tokens, etc.) suivent le même principe ; les réglages du modèle remplacent les valeurs correspondantes de la connexion.

Cliquer sur le modèle dans le compositeur ouvre le réglage d’effort lorsqu’il est disponible ; cliquer sur son nom dans ce panneau ouvre la liste des modèles. Les niveaux proviennent du catalogue du fournisseur ou, pour vLLM, du template du tokenizer du serveur. Si ce dernier n’est pas publié, 1loop consulte le template public du dépôt indiqué par le serveur ; sa source figure dans les paramètres du modèle. Un template modifié côté serveur peut donc exposer des capacités différentes. Sans métadonnées connues, aucun réglage d’effort n’est affiché.

Les réglages s’ouvrent en pleine fenêtre : leurs sections remplacent la barre latérale, la section choisie remplace la conversation, et une connexion ou un modèle s’ouvre comme une page à la place de la précédente (Échap ou le lien en haut pour revenir). La plupart des réglages s’appliquent dès qu’ils changent ; une connexion s’édite comme un brouillon, enregistré avec les boutons fixés en bas de sa page.

**Settings → General** règle le workspace et les permissions. Les dossiers et images sont choisis avec le sélecteur du bureau (portail XDG, sinon zenity ou kdialog). **Instructions** ajoute des consignes utilisateur au prompt système existant, pour les prochaines requêtes de tous les chats.

## Réponses

Les réponses sont mises en page par 1loop lui-même (`markdown.go`, `mdinline.go`, `flowtext.go`) : titres, paragraphes, listes à puces, numérotées et imbriquées, cases à cocher, citations, filets, tableaux, blocs de code avec leur bouton de copie, gras, italique, barré, code en ligne sur fond teinté, liens `[texte](adresse)` et adresses nues, cliquables (ouverts par l’application du bureau).

**Formules** : `$…$`, `\(…\)`, `$$…$$` et `\[…\]` sont composées par le TeX de l’ordinateur quand `latex` et `dvipng` sont installés (paquets `texlive-latex-extra` et `dvipng` sous Debian), avec `amsmath` et `amssymb`. Elles sont calculées à côté de l’interface puis gardées dans `~/.cache/1loop/math`. Sans TeX, ou si une formule ne compile pas, sa source reste affichée comme du code. TeX tourne sans accès au shell et sans lecture de fichiers hors de son dossier de travail.

**Sélection** : le texte d’une réponse se sélectionne à la souris, d’un paragraphe à l’autre, code et tableaux compris ; double-clic pour un mot, Ctrl+C pour copier, Échap pour annuler.

## Outils

Les connexions utilisent quatre outils exécutés par le backend Go :

- `read_file` : lecture d’un fichier texte, jusqu’à 1 MiB.
- `list_directory` : liste des fichiers d’un dossier.
- `shell` : commande Bash, avec aperçu et validation, délai de 60 secondes et sortie limitée à 128 KiB.
- `edit_file` : remplacement exact unique ou création d’un fichier, avec diff unifié avant validation. Les suppressions passent par `shell` (`rm`, `mv`…), limitées au workspace par le prompt, avec validation hors mode YOLO.

Le menu **Approval required** du compositeur demande une validation avant chaque commande ou édition. **YOLO mode** les approuve automatiquement ; ce réglage est également accessible dans Settings. Avec un modèle Claude Code, un troisième choix apparaît : **Auto mode**, le mode de permission « auto » de Claude Code, qui exécute ce qu’il juge sûr et relaie le reste à l’utilisateur. Avec un autre modèle, ce réglage revient à **Approval required**.

Les lectures et éditions sont limitées au workspace, y compris la résolution des liens symboliques. Le shell s’exécute avec les droits de l’utilisateur : **la validation d’une commande lui permet d’agir sur la machine**, sans sandbox supplémentaire.

Les conversations, projets et réglages sont conservés dans `~/.config/1loop/state.json`, hors du projet (un ancien `.1loop/state.json` est migré automatiquement). Les clés saisies restent en mémoire et doivent être renseignées à nouveau après redémarrage. Les jetons OAuth sont enregistrés dans `~/.config/1loop/openai-<profil>.json` (ou `openai.json` pour le compte historique) avec des permissions 0600 et renouvelés par 1loop. Aucune conversation Codex n’est créée. Les anciens historiques locaux sont conservés ; une nouvelle autorisation OAuth est nécessaire pour OpenAI.

## Télécommande Android

`android/` contient l'application Android native (Kotlin + Jetpack Compose) qui pilote 1loop depuis un téléphone : même style, même logo, même animation de lancement et même roue de travail que l'application de bureau.

- 1loop sert ses conversations sur le réseau local : découverte en broadcast UDP (port 47100), API JSON et événements en direct (port 47101). Désactivable dans **Settings → Phone remote**, où l'on retire aussi les téléphones appairés.
- Au premier contact, 1loop affiche sur l'ordinateur un code à six chiffres à saisir sur le téléphone. Seule l'empreinte du jeton est conservée ; sans jeton, aucune requête n'aboutit (cinq essais par code).
- Ce que le téléphone envoie passe par les mêmes chemins que la fenêtre : la conversation s'ouvre et s'affiche sur l'ordinateur. Les approbations peuvent se donner sur l'un ou l'autre.
- Le trafic circule en HTTP sur le réseau local : à réserver à un réseau de confiance.

```bash
cd android && ./gradlew assembleDebug   # JDK 17+, SDK Android (compileSdk 37)
adb install -r app/build/outputs/apk/debug/app-debug.apk
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
