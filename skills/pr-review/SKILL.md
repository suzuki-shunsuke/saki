---
name: pr-review
description: When asked to review a pull request created by someone else, generates documents explaining the pull request's overview, specification, and code so that a human without its context can review it, and presents an agent's review results alongside them. Use when asked to review or understand a pull request. Do not use for pull requests you created and manage with dev-flow.
---

Run `saki skill show pr-review` and follow the instructions it prints.
When they refer to a file of the skill, such as `templates/review.md`, read it with `saki skill show pr-review <path>`.

The instructions are embedded in saki, so they always match the installed saki.
If saki isn't installed, install it first: https://github.com/suzuki-shunsuke/saki
