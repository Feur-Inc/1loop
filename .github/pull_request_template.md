<!--
Rules for every pull request:

1. Cite the issue it fixes or adds: "Closes #N" for a fix or a finished
   feature, "Part of #N" when it only covers some of the issue.

2. Target `dev`, never `main`: `main` only receives `dev` when it is
   released. `gh pr create` defaults to `main`, so pass `--base dev`.

3. Fill in the two description sections below:
   - "Summary": the feature explained without code, as a list, one step per
     item, in the order things happen. Simple words, but no information
     lost: name the real places and names (the file on disk and its
     format, the settings page, the tool call, what the user sees on the
     desktop and on the phone).
   - "Technical details": for reviewers, what changed in the code and why.

4. The screenshot table is MANDATORY for every visible change (desktop or
   phone interface, the site, anything a screenshot can show): a before
   and an after of the same screen, shown in a table in the description.
   The table's shape is free (one row per screen, more columns for more
   steps), but it must be a table with the images in it. Only when nothing
   is visible, write "No visible change." instead of the table.

5. Attach the images with GitHub CLI 2.99.0 or newer (`gh --version`).
   Keep them outside the repository (for example in /tmp/), never commit
   them to display them. Reference their local paths in the body; `--attach`
   uploads each file and replaces its path with the GitHub-hosted URL:

     | Before | After |
     | ------ | ----- |
     | ![Before](/tmp/before.png) | ![After](/tmp/after.png) |

     gh pr create \
       --base dev \
       --title "PR title" \
       --body-file /tmp/pr-body.md \
       --attach /tmp/before.png \
       --attach /tmp/after.png

   On an existing pull request:

     gh pr edit NUMBER --body-file /tmp/pr-body.md \
       --attach /tmp/before.png --attach /tmp/after.png

   In a comment: gh pr comment NUMBER --body "Screenshot" --attach "/tmp/shot.png#Screenshot"

6. Always include an "Assisted-by:" footer with the exact model and version
   actually used, plus its reasoning effort when applicable, for example:
   "Assisted-by: GPT-6.1 medium". A tool name alone (Codex, Claude Code,
   etc.) is not a precise model attribution.
-->

Closes #

## Summary

<!--
Without code, one step per item, nothing left out. For example:
- The agent runs a command with the `shell` tool call.
- While it runs, 1loop reads its output four times a second and sends the
  last 16 KiB to the conversation.
- On the desktop, clicking the call opens the side panel, where the output
  grows as it arrives; on the phone, the call's sheet does the same.
- The model still receives the whole output only when the command ends.
- Nothing new is saved: the conversation keeps the final output, as before.
-->

- 

## Screenshots

| Before | After |
| ------ | ----- |
|        |       |

## Technical details

<!-- Files and functions changed, new events, APIs or formats, choices made and why. -->

- 

Assisted-by: 
