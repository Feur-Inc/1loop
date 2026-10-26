<!--
Règles pour chaque pull request (PR), y compris si elle est créée avec
`gh pr create --body` ou `gh pr create --body-file` :

1. Référence à une issue :
   - `Closes #N` pour une correction ou une fonctionnalité terminée.
   - `Part of #N` si la PR ne traite qu'une partie de l'issue.
   - `Aucune issue associée.` s'il n'existe pas d'issue correspondante.
   Remplacez N par le véritable numéro ; ne laissez pas `Closes #` vide.
   Attention : sur ce dépôt, `main` est la branche par défaut. Une PR
   ciblant `dev` ne ferme PAS automatiquement l'issue avec `Closes #N` :
   GitHub n'interprète ces mots-clés que pour les PR ciblant la branche
   par défaut. Suivez l'issue jusqu'à sa fermeture au moment approprié.

2. Branche cible : toujours `dev`, jamais `main`. La branche `main` ne
   reçoit `dev` que lors d'une publication. `gh pr create` utilise
   normalement la branche par défaut (`main`) en l'absence de réglage
   particulier : précisez donc toujours `--base dev`.

3. Remplissez les trois sections de la description :
   - « Résumé » : expliquez ce qui change sans détailler le code, avec
     une liste d'étapes dans leur ordre réel. Utilisez des mots simples
     sans omettre les informations utiles : fichier et format enregistrés,
     page de réglages, appel d'outil, résultat visible sur ordinateur et
     sur téléphone, selon les éléments concernés.
   - « Captures d'écran » : appliquez la règle 4 ci-dessous.
   - « Détails techniques » : indiquez aux personnes qui relisent la PR
     les fichiers, fonctions, événements, API et formats modifiés, ainsi
     que les raisons des choix effectués.

4. Pour tout changement visible (application ordinateur ou téléphone,
   site, interface GitHub, ou autre écran), fournissez un tableau Markdown
   contenant au minimum une capture « Avant » et une capture « Après »
   DU MÊME écran. Ajoutez des lignes ou des colonnes si nécessaire.
   Si rien n'est visible, SUPPRIMEZ le tableau et écrivez uniquement
   `Aucun changement visible.`. Ne laissez jamais un tableau vide et
   n'inventez pas de captures ; signalez toute capture impossible.

5. Joignez les images avec GitHub CLI version 2.99.0 ou plus récente
   (vérifiez avec `gh --version`). Les images destinées à la PR doivent
   rester hors du dépôt (par exemple dans `/tmp/`) : ne les ajoutez pas
   à des commits uniquement pour les afficher.

   Dans le corps de la PR, référencez les mêmes chemins locaux que ceux
   passés à `--attach`. GitHub CLI téléverse chaque image et remplace
   son chemin Markdown par l'URL de l'image hébergée sur GitHub :

     | Avant | Après |
     | ----- | ----- |
     | ![Avant](/tmp/avant.png) | ![Après](/tmp/apres.png) |

   Créer une PR (après avoir enregistré le corps dans /tmp/pr-body.md) :

     gh pr create \
       --repo Feur-Inc/1loop \
       --base dev \
       --title "Titre de la PR" \
       --body-file /tmp/pr-body.md \
       --attach /tmp/avant.png \
       --attach /tmp/apres.png

   Modifier une PR existante :

     gh pr edit NUMERO --repo Feur-Inc/1loop \
       --body-file /tmp/pr-body.md \
       --attach /tmp/avant.png \
       --attach /tmp/apres.png

   Ajouter une image à un emplacement précis dans un commentaire :

     gh pr comment NUMERO --repo Feur-Inc/1loop \
       --body "![Capture](/tmp/capture.png)" \
       --attach '/tmp/capture.png#Capture'

   Si un fichier transmis à `--attach` n'est PAS référencé dans le
   Markdown du corps ou du commentaire, GitHub CLI l'ajoute à la fin.
   Le suffixe `#Description` indique le texte alternatif des images
   ajoutées automatiquement ; lorsqu'une image est déjà référencée
   dans le Markdown, c'est le texte entre `![...]` qui est conservé.
   Les téléversements nécessitent un accès en écriture au dépôt.
   Documentation : https://docs.github.com/en/github-cli/github-cli/attaching-files-with-github-cli

6. Terminez toujours par `Assisted-by:` avec le nom exact et la version
   réelle de chaque modèle d'IA utilisé, en précisant l'effort de
   raisonnement lorsque cette information est disponible. Exemple de
   format uniquement : `Assisted-by: GPT-6.1 medium` ; ne recopiez pas
   un modèle fictif ou non utilisé. « Codex » ou « Claude Code » seuls
   désignent des outils, pas des versions exactes de modèle.
   Si aucune IA n'a été utilisée, indiquez `Assisted-by: Aucun`.
-->

<!-- Remplacer la ligne suivante par « Closes #N », « Part of #N » ou « Aucune issue associée. ». -->
Issue : À renseigner.

## Résumé

<!--
Expliquez les changements du point de vue de l'utilisateur, sans jargon
technique inutile : une étape par ligne, dans l'ordre où elle se produit.
Nommez les écrans, chemins de fichiers, formats et comportements réels.
Supprimez ces instructions et remplacez le tiret par votre résumé.
-->

- À compléter.

## Captures d'écran

<!--
SI le changement est visible, supprimez « Aucun changement visible. »
et mettez à sa place un véritable tableau contenant des images avant/après
prises sur le même écran. Exemple (à adapter aux fichiers existants) :

| Avant | Après |
| ----- | ----- |
| ![Avant](/tmp/avant.png) | ![Après](/tmp/apres.png) |

Joignez les deux fichiers avec `gh pr create/edit --attach` ; ils doivent
rester hors du dépôt. SI aucun changement n'est visible, conservez
seulement la phrase ci-dessous et ne mettez aucun tableau vide.
-->

Aucun changement visible.

## Détails techniques

<!-- Précisez les fichiers/fonctions modifiés, les formats, API ou événements, et pourquoi. -->

- À compléter.

Assisted-by: À renseigner.