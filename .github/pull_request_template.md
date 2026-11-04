<!--
Rules for every pull request (PR), including one created with
`gh pr create --body` or `gh pr create --body-file`:

1. Language: write every pull request and every issue in English only:
   title, description, comments and commit messages. No French anywhere,
   even when the request was written in French.

2. Issue reference:
   - `Closes #N` for a fix or a finished feature.
   - `Part of #N` when the PR covers only part of the issue.
   - `No related issue.` when there is no matching issue.
   Replace N with the real number; never leave `Closes #` empty.
   Note: on this repository, `main` is the default branch. A PR targeting
   `dev` does NOT close the issue automatically with `Closes #N`: GitHub
   reads these keywords only for PRs targeting the default branch. Follow
   the issue until it is closed at the right time.

3. Target branch: always `dev`, never `main`. `main` only receives `dev`
   for a release. `gh pr create` normally uses the default branch (`main`)
   unless told otherwise: so always pass `--base dev`.

4. Fill in the three sections of the description:
   - "Summary": explain what changes without detailing the code, as a list
     of steps in their real order. Use simple words without leaving out
     useful information: saved file and format, settings page, tool call,
     visible result on desktop and on the phone, as relevant.
   - "Screenshots": apply rule 5 below.
   - "Technical details": tell reviewers which files, functions, events,
     APIs and formats changed, and why these choices were made.

5. For any visible change (desktop or phone app, site, GitHub interface,
   or any other screen), provide a Markdown table with at least a "Before"
   and an "After" screenshot OF THE SAME screen. Add rows or columns when
   needed. If nothing is visible, DELETE the table and write only
   `No visible change.`. Never leave an empty table and never invent
   screenshots; say when a screenshot was not possible.

6. Attach images with GitHub CLI version 2.99.0 or later (check with
   `gh --version`). Images meant for the PR must stay outside the
   repository (for example in `/tmp/`): do not add them to commits only
   to display them.

   In the PR body, reference the same local paths as the ones passed to
   `--attach`. GitHub CLI uploads each image and replaces its Markdown path
   with the URL of the image hosted on GitHub:

     | Before | After |
     | ------ | ----- |
     | ![Before](/tmp/before.png) | ![After](/tmp/after.png) |

   Create a PR (after saving the body in /tmp/pr-body.md):

     gh pr create \
       --repo Feur-Inc/1loop \
       --base dev \
       --title "PR title" \
       --body-file /tmp/pr-body.md \
       --attach /tmp/before.png \
       --attach /tmp/after.png

   Edit an existing PR:

     gh pr edit NUMBER --repo Feur-Inc/1loop \
       --body-file /tmp/pr-body.md \
       --attach /tmp/before.png \
       --attach /tmp/after.png

   Add an image at a precise place in a comment:

     gh pr comment NUMBER --repo Feur-Inc/1loop \
       --body "![Screenshot](/tmp/screenshot.png)" \
       --attach '/tmp/screenshot.png#Screenshot'

   If a file passed to `--attach` is NOT referenced in the Markdown of the
   body or the comment, GitHub CLI adds it at the end. The `#Description`
   suffix sets the alt text of images added automatically; when an image
   is already referenced in the Markdown, the text inside `![...]` is
   kept. Uploads require write access to the repository.
   Documentation: https://docs.github.com/en/github-cli/github-cli/attaching-files-with-github-cli

7. Always end with `Assisted-by:` and the exact name and real version of
   each AI model used, with its reasoning effort when known. Format
   example only: `Assisted-by: GPT-6.1 medium`; do not copy a made-up or
   unused model. "Codex" or "Claude Code" alone name tools, not exact
   model versions. If no AI was used, write `Assisted-by: None`.

8. License header: every new source file of 1loop (Go, Kotlin, Gradle
   `.kts`, C, Python, PowerShell, shell, Nix, extensionless scripts)
   starts with a `SPDX-License-Identifier: MPL-2.0` line, as added by
   #160: a `//` comment for Go, Kotlin and C, `#` for the others. A
   shebang stays on line 1 and the header goes right below it. In Go, a
   blank line follows the header so it does not become the package doc
   comment and `//go:build` lines still work. Files under `third_party/`
   and `android/third_party/`, `android/gradlew`, and license or asset
   text files keep no header.
-->

<!-- Replace the next line with "Closes #N", "Part of #N" or "No related issue.". -->
Issue: To fill in.

## Summary

<!--
Explain the changes from the user's point of view, without needless
technical jargon: one step per line, in the order it happens. Name the
real screens, file paths, formats and behaviors.
Delete these instructions and replace the dash with your summary.
-->

- To fill in.

## Screenshots

<!--
IF the change is visible, delete "No visible change." and put in its
place a real table with before/after images taken on the same screen.
Example (adapt it to the real files):

| Before | After |
| ------ | ----- |
| ![Before](/tmp/before.png) | ![After](/tmp/after.png) |

Attach both files with `gh pr create/edit --attach`; they must stay
outside the repository. IF nothing is visible, keep only the sentence
below and no empty table.
-->

No visible change.

## Technical details

<!-- Give the changed files/functions, formats, APIs or events, and why. -->

- To fill in.

Assisted-by: To fill in.