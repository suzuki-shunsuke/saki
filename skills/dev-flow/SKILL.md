---
name: dev-flow
description: Defines how to go from design through implementation, pull request creation, review, and merge. Avoids problems common in AI-driven implementation and review, such as humans being unable to understand the changes, or pull requests drifting from their original scope and bloating. Use when a human asks for an implementation large enough to need design, when asked to investigate or organize information to decide what to do, or when you created a pull request without design and want to bring it into the flow. Do not use for small changes such as typo fixes, or for reviewing pull requests created by others (use the pr-review skill).
---

Run `saki skill show dev-flow` and follow the instructions it prints.
When they refer to a file of the skill, such as `templates/design.md`, read it with `saki skill show dev-flow <path>`.

The instructions are embedded in saki, so they always match the installed saki.
If saki isn't installed, install it first: https://github.com/suzuki-shunsuke/saki
