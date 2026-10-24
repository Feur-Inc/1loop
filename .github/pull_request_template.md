<!--
Rules for every pull request:

1. Cite the issue it fixes or adds: "Closes #N" for a fix or a finished
   feature, "Part of #N" when it only covers some of the issue.
2. When the change is visible (desktop or phone interface, the site,
   anything a screenshot can show), add a before and an after screenshot,
   taken on the same screen.
3. Target `dev`, never `main`: `main` only receives `dev` when it is
   released. `gh pr create` defaults to `main`, so pass `--base dev`.

`gh pr create --body` skips this template: follow these rules anyway. `gh`
cannot upload images, so never commit screenshots to the repository; say in
the description that they still have to be attached.
-->

Closes #

## What changed



## Screenshots

<!-- Visible change: fill in both. Nothing visible: delete this section. -->

| Before | After |
| ------ | ----- |
|        |       |
