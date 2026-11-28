---
name: pr-review
summary: Write documents for a human to review a pull request created by someone else
description: When asked to review a pull request created by someone else, generates documents explaining the pull request's overview, specification, and code so that a human without its context can review it, and presents an agent's review results alongside them. Use when asked to review or understand a pull request. Do not use for pull requests you created and manage with dev-flow.
---

## Principles

- The goal is to support human review. A human without the pull request's context should be able to judge and comment appropriately just by reading the documents
- The agent does not make review decisions on the human's behalf. Present the agent's review results as material for the human's judgment, and do not comment on, approve, or request changes on the pull request without the human's approval
- Distinguish facts read from the pull request from the agent's guesses. Mark anything written from a guess as a guess, and turn it into a question for the pull request author (Questions for Author)

## Starting the Flow

Start this flow when a human asks you to review or understand a pull request, even if the skill is not explicitly invoked.

Pull requests created and managed with dev-flow are out of scope: a pull request listed in `pull_requests` of a task in tasks/ whose `type` isn't `pr-review`. For those, follow the dev-flow flow (`saki skill show dev-flow`). A review's own task (`type: pr-review`) doesn't make its pull request out of scope.

1. Look for the review of the pull request in `tasks/<review id>/` (see "Review ID"). If you are given the directory of a review but not the pull request, as when a session is started with the review's new or open button in `saki serve`, get the pull request from `pull_requests` in its task.yaml, and ask the human if there is none
1. If there is no such directory, start from step 1 of Steps
1. If the directory has no task.yaml (the review stopped in step 1 before creating it), create task.yaml (see "task.yaml") and continue step 1
1. Otherwise, append this session's ID to `claude_sessions` (see "task.yaml"), and resume according to `status`
    - `doing` and details.md records that "When the Pull Request Is Updated" started without recording that it finished (the review stopped during the update): follow "When the Pull Request Is Updated" again from the beginning, with the diff from the Reviewed Head to the pull request's current head
    - `doing` otherwise (the review stopped in steps 1-4): determine the progress from which sections are filled in and the record in details.md, and continue from the next step. If review.md has no Reviewed Head yet, continue step 1. If it has one and the pull request's head is newer, first move to the new head: record the change in details.md, update the documents already written according to the diff (`git diff <reviewed head>..<new head>`), point the worktree and the code links at the new head, and update Reviewed Head. Then continue from the step where the review stopped. Don't use "When the Pull Request Is Updated", which is for reviews that finished step 4 and returns to step 5
    - `in-review` or `done`: compare the Reviewed Head recorded in the documents with the pull request's current head. If the pull request has been updated, follow "When the Pull Request Is Updated"; otherwise resume from step 5 of Steps
    - `pending` or `cancel`: ask the human whether to resume the review
    - In any case, if the work needs the worktree and it no longer exists (it was removed by "Cleaning Up a Finished Review"), check out the Reviewed Head (the new head when following "When the Pull Request Is Updated") with git worktree again, and record its path in details.md (see "Recording the Worktree")

Work on each review in its own herdr pane.

- If your prompt contains "This pane is dedicated to this task. Don't open another pane.", you are already in that pane: proceed here without opening another pane
- Otherwise, open a new pane in herdr, start Claude Code there, and give it the prompt `Run "saki skill show pr-review" and follow the instructions. This pane is dedicated to this task. Don't open another pane.` followed by the request

## Document Layout

```
tasks/
  <review id>/
    task.yaml
    review.md
    details.md
    pr-explanation.md
```

A review is a kind of task: it sits in tasks/ beside the tasks of dev-flow, with `type: pr-review` in its task.yaml, so that `saki serve` lists it with the other tasks.

- `task.yaml`: the review's metadata, generated from `saki skill show pr-review templates/task.yaml` (see "task.yaml")
- `review.md`: the main document, generated from `saki skill show pr-review templates/review.md`. It summarizes the overview, specification, and review results, and is what the human reads first
- `details.md`: the record of primary information and decisions. Write the collected information (commands and the gist of their output, URLs of referenced issues, related pull requests, Slack threads, etc.), the evidence behind guesses, and for each finding of the agent's review whether it was adopted or rejected and why. Keep it separate from review.md because it tends to grow large
- `pr-explanation.md`: the PR Explanation (see "PR Explanation")

### Review ID

The review id is `review_<owner>_<repo>_<pr number>`, with the owner and the repository in lower case. For example, the review of `https://github.com/Suzuki-Shunsuke/tfaction/pull/4404` is in `tasks/review_suzuki-shunsuke_tfaction_4404/`.

- The id follows from the pull request, so don't ask the human to confirm it
- It's unique: GitHub owners can't contain `_`, and the pull request number is digits only. The owner and the repository are lower-cased because GitHub ignores their case, so that differently written URLs of one pull request don't make two reviews
- Don't rename the herdr tab after the review id. A review started from `saki serve` runs in a tab labeled `review <owner>/<repo>#<number>`, which `saki serve` uses to find the tab already reviewing the pull request

### task.yaml

Create task.yaml from `saki skill show pr-review templates/task.yaml` right after getting the pull request's information with `gh pr view` in step 1, before writing any other document, so that the review shows up in `saki serve` as soon as it starts.

| Key | Value |
|-----|-------|
| `name` | the review id |
| `description` | `Review <owner>/<repo>#<pr number>: <pull request title>` |
| `type` | `pr-review` |
| `status` | see below |
| `tags` | may be empty (the human may add tags) |
| `assignees` | the one login `saki assignee` prints (see below) |
| `claude_sessions` | the sessions that worked on the review |
| `repositories` | `https://github.com/<owner>/<repo>` |
| `issues` | the URLs of the issues the pull request closes (`closingIssuesReferences`), or empty |
| `pull_requests` | `https://github.com/<owner>/<repo>/pull/<pr number>` |
| `parent_task`, `sub_tasks`, `derived_from`, `derived_tasks` | empty |

Run `saki assignee` when creating task.yaml, and set `assignees` to a list of the one login it prints. This tells whose review a review is when tasks/ is shared with git. If it fails, leave `assignees` empty (`[]`), tell the human in a sentence with its error, and continue. Don't retry. Don't change `assignees` afterwards, even when you resume a review created by someone else, and don't add it to a task.yaml that has none. The human edits it to add or change the people in charge.

Every session that works on the review appends its ID (the environment variable `CLAUDE_CODE_SESSION_ID`) and a summary of the session to the end of `claude_sessions`, when it creates task.yaml or when it resumes the review. Skip it if the session is already listed. This lets the review's open button in `saki serve` focus the session's pane.

Update `status` as the review progresses.

| When | status |
|------|--------|
| task.yaml is created in step 1, through step 4 (the agent works autonomously) | `doing` |
| Entering step 5, and during step 6 | `in-review` |
| The human says the review is finished (the comments are posted, the review is no longer needed, etc.) | `done` |
| "When the Pull Request Is Updated" starts (even from `done`) | `doing`, then `in-review` on returning to step 5 |
| A `done` review is resumed without an update of the pull request (to reread the documents or ask questions) | stays `done`. Set `in-review` only when the human asks to redo the review |

Use `pending` and `cancel` only when the human says so.

Once a review is `done` or `cancel` and nothing is left for the human, clean it up (see "Cleaning Up a Finished Review").

## Steps

### 1. Collect Information

1. Get the pull request's information
    - `gh pr view <number> -R <owner>/<repo> --json title,body,author,baseRefName,headRefName,headRefOid,files,additions,deletions,labels,closingIssuesReferences,url`
    - Right after this, create `tasks/<review id>/task.yaml` and append this session's ID to `claude_sessions` (see "task.yaml")
    - `gh pr diff <number> -R <owner>/<repo>`
    - Existing review comments and conversations (`gh pr view --comments`, `gh api repos/<owner>/<repo>/pulls/<number>/comments`)
    - CI results (`gh pr checks`)
1. Collect the intent and history of the change from the pull request's description, linked issues, and related pull requests
1. The diff alone does not show callers or surrounding context, so check out the pull request's head with git worktree and read the code. Record the worktree's path in details.md (see "Recording the Worktree")
1. Base everything written from here on on the head SHA at this point. Record it as Reviewed Head in review.md

Record the collected information and its sources in details.md.

### 2. Write the Overview and Specification

Generate the document from `saki skill show pr-review templates/review.md` and write Background / What / Why / Scope / Out of Scope / Behaviour.

#### How to Write the Overview

The overview lets a reader judge the problem the pull request tries to solve and its direction without reading the code.

- Background: write what the reader needs to know as a premise. What the related features or commands do (e.g. what `aqua vacuum` is, what `--init` is for), the history so far, and so on. Make sure the human does not end up asking "wait, what was this again?"
- What / Why: write in terms of behaviour as seen by users. Do not write about internal implementation such as function or type names. If needed, write those in the PR Explanation or details.md
  - When the change itself is about internal implementation, such as refactoring, it is fine to talk about internal implementation. Even then, make sure the reader can grasp the premise from Background
- For changes that alter behaviour, write Before / After in What to contrast what changes and how (e.g. the current result and the result after the change when running the same command)
- Where possible, write the current problem in Why as bug reproduction steps to make it easy to picture
  - How To Reproduce: the steps (example configuration files and commands)
  - Expected Behaviour: the expected behaviour
  - Actual Behaviour: the actual behaviour
  - You may omit these for changes they do not fit, such as new features or improvements
- Write Scope / Out of Scope / Behaviour in terms of behaviour as much as possible too. Put function names and the like in the PR Explanation

For pull requests by others, also follow these rules.

- Do not copy the pull request's description as is. Fill in premises the description does not state (what the related features do, the history) so that someone without the context can read it
- Explicitly note places where the description and the implementation disagree, as disagreements
- In Scope, write what the pull request actually changes; in Out of Scope, write what the pull request intentionally does not change (if the description or issue says so)
- If the pull request includes changes outside its purpose (incidental refactoring, changes to unrelated files, etc.), write them in "Out-of-Scope Changes"
- Mark anything written from a guess with `(guess)` and write the evidence in details.md. Write anything you cannot confirm in Questions for Author

### 3. Write the PR Explanation

Write pr-explanation.md following "PR Explanation". In the PR Explanation section of review.md, write only the TL;DR and a link to pr-explanation.md.

### 4. Have Agents Review

1. Pull request review: have a subagent with a fresh context review the pull request, given only the pull request (URL, diff, checked-out code). Do not pass the documents or your own interpretation. Passing the writer's context drags it toward the writer's conclusions
    - Aspects: correctness (bugs, boundary conditions, error handling), whether the change is sufficient for the pull request's purpose and contains nothing extra, whether it breaks compatibility, security, and the validity of tests
    - Have it report only findings that need fixing and questions to confirm with the pull request author. Do not have it report matters of taste or nits
1. Verify the findings: for each finding, read the code to check whether it really holds. Reject findings that do not hold and write the reason in details.md
1. Write the findings that hold in Review Findings in review.md (see "How to Write Review Findings")
1. Document review: give review.md, pr-explanation.md, and the pull request to another subagent with a fresh context, and have it check whether the documents explain the pull request correctly
    - Statements that differ from the facts, important changes in the pull request that are not explained, and guesses not marked with `(guess)`
    - Whether the documents follow "How to Write the Overview" (whether premises are written, whether they avoid talking in terms of internal implementation)
1. Reflect the findings on the documents

Run the pull request review and the document review once each. Do not repeat them until they converge as in dev-flow. Reviewing does not change the pull request's code, so repeating only increases the variance of the findings. If, after reflecting the findings, there are places in the documents whose correctness you are not confident about, tell the human when handing over.

#### How to Write Review Findings

For each finding, write the following.

- Severity
  - `must`: should be fixed before merging (bugs, data corruption, security, breaking compatibility, etc.)
  - `should`: better fixed, but the merge decision is left to the human
  - `question`: the intent cannot be read and you want to confirm with the pull request author
- Location: a GitHub link pinned to a commit SHA
- Problem: what is wrong
- Evidence: the concrete input or state that causes the problem, and its result
- Suggestion: a proposed fix (if any)

Write improvement ideas outside the pull request's purpose in Follow-ups, not in Review Findings.

### 5. Hand Over to the Human

1. Set `status` to `in-review`, unless a `done` review was resumed without an update of the pull request (see "task.yaml")
1. Open the documents with `mo` in the review's group (`mo --target <review id> tasks/<review id>/review.md tasks/<review id>/pr-explanation.md`). The group is named after the review's directory, the same group `saki serve`'s `mo` button opens, so the documents don't pile up in the `default` group mixed with other tasks'
1. The human reads the documents and asks questions in conversation with the agent. The agent answers and reflects the answers in the documents if needed (mo reloads automatically on save)
1. The human decides which Review Findings to comment on the pull request, and whether to Approve, Request changes, or Comment

### 6. Post Comments (the Human Approves)

1. Draft review comments from the findings the human chose, and show them to the human. The pull request author reads them, so do not reuse the documents' internal notation (`(guess)`, severity labels, etc.) as is; write prose that conveys the evidence and suggestion.
1. Post them only after getting the human's approval. Approve or Request changes only when the human explicitly instructs it
1. Record the URLs of the posted comments in details.md and update the status of the Review Findings
1. When the human says the review is finished, set `status` to `done`
1. Once nothing is left for the human, clean the review up (see "Cleaning Up a Finished Review")

## When the Pull Request Is Updated

1. Set `status` to `doing`, and record in details.md that the update from the Reviewed Head to the new head started, so that a session resuming the review knows it stopped during the update (see "Starting the Flow")
1. Check the diff from the Reviewed Head to the new head (`git diff <reviewed head>..<new head>` or GitHub's compare). If reading the code needs the worktree and it no longer exists, check out the new head with git worktree again and record its path in details.md (see "Recording the Worktree")
1. Update review.md and pr-explanation.md according to the diff. Repoint code links to the new head's SHA
1. Have an agent review the added diff under the same rules as step 4 of Steps. Also check whether existing Review Findings have been resolved, and update their status
1. Update Reviewed Head to the new head, and record what changed and that the update finished in details.md
1. Resume from step 5 of Steps

## Cleaning Up a Finished Review

When a review is finished, remove its worktrees and close the herdr pane the session runs in, so that finished reviews don't pile up as panes and tabs in herdr and as worktrees on disk.

### When to Clean Up

At the end of a turn responding to a human message, clean up only when all of the following hold. Otherwise don't, and wait for the human's next message as usual.

1. The review's `status` is `done` or `cancel`
1. Nothing is left for the human to decide or answer: no question to the human is pending
1. This turn's reply has nothing the human needs to read, such as an answer to a question, material for a decision, or a report of a failure. If it has, don't clean up in this turn (don't remove the worktrees either): reply, end the reply with a sentence saying that sending any message will clean the review up, and wait. Clean up in a later turn whose reply has nothing to read (a short reply such as "ok" from the human is enough)
    - Things already recorded in the documents, such as a report that the review was set to `done` or `cancel` or the URLs of posted comments, don't count as something to read
1. This session won't go on to work on another task afterwards

This is not limited to the session that set `done` or `cancel`. A session that reopens a finished review to reread documents or ask questions follows the same conditions: it doesn't clean up in a turn that answers a question, and cleans up in a turn with nothing to read.

However, don't clean up in the turn responding to the prompt that starts the session (the prompt sent by `saki serve`, or the human's first request), because the human may have opened the review to reread it or ask questions. Clean up only in a turn after the human has sent another message in the session.
When a session that cleaned up is resumed (for example by `saki serve`'s open button, which sends no prompt), treat the human's first message after the resume the same as a prompt that starts the session, and don't clean up in the turn responding to it. You can tell such a session because its last turn ended by closing the pane, or because details.md records that the review was cleaned up and the pane closed.

### What to Do

Do the following in this order.

1. Remove every worktree whose path is recorded in details.md (see "Recording the Worktree"). Pass the worktree to `-C`, because tasks/ and the worktree are usually in different repositories and the command must work whatever the current directory is. Don't pass `--force`

    ```sh
    test -d <path> && git -C <path> worktree remove <path>
    ```

    - If the directory no longer exists (`test -d <path>` is false), skip that path. It doesn't count as a failure
    - Don't remove worktrees that aren't recorded in details.md, such as those of reviews started before this rule: the agent didn't necessarily create them, and they may be the human's. Say nothing about them
    - If any worktree can't be removed (git refuses because of uncommitted changes or untracked files, for example), don't go on to step 3 (don't close the pane). Record in details.md the worktrees removed so far, tell the human which worktree couldn't be removed and why, and wait. Don't pass `--force` or discard the changes on your own judgment. After handling the human's instruction, follow "When to Clean Up" again
1. Record the cleanup in details.md: the date, the paths of the removed worktrees, and, if step 3 will close the pane, that the pane is closed. The human can't read your last message once the pane is closed, so the record is kept in the document
1. Last, close your herdr pane. If the tab has no other pane, herdr closes the tab too; otherwise the tab stays. Closing the pane stops you, so put nothing after it

    ```sh
    herdr pane close "$HERDR_PANE_ID"
    ```

    - Only an agent whose prompt contains "This pane is dedicated to this task. Don't open another pane." closes its pane. An agent without that sentence, or without the environment variable `HERDR_PANE_ID` (running outside herdr), doesn't close any pane, and says nothing about it
    - If `herdr` isn't found or returns an error and the pane isn't closed, you are still running: add to details.md that the pane wasn't closed, so that the record matches what happened, and tell the human in a sentence. Don't retry

### Recording the Worktree

- When you create a worktree for the review (step 1 of Steps, or when recreating one on resuming the review), record its absolute path in details.md, so that `git -C <path>` points at the same worktree whatever the current directory is
- If a review creates more than one worktree (recreated on resuming, for example), record each of them

## PR Explanation

Purpose:

- Assist the human who reads the code in reviewing it. While review.md explains "what the pull request does", the PR Explanation explains "how the code works"

What to write:

How to write it depends on the pull request, but code changes generally include the following.

- At the top, write a short summary of the changes per module unit (Package in Go)
- Structure the body around the order in which the code is read (the execution flow), and in that order describe the role, callers, and changes of each changed function, type, etc.

What to write in every pull request:

- Places to look at
  - Links to the `must` / `should` Review Findings
  - Places where the implementation disagrees with the pull request's description or issue
  - Places that are not findings but that the reviewer should understand (changes with side effects or a wide impact, behaviour that is hard to follow)
- Tests: what the tests in the pull request confirm and what they do not, and the CI results

How to write:

- Only refer to Behaviour in review.md for the specification; do not duplicate it. Focus on explaining the code
- Point to code locations with GitHub links pinned to a commit SHA (Reviewed Head) (`https://github.com/<owner>/<repo>/blob/<commit SHA>/<path>#L<start>-L<end>`), so the links do not drift when the pull request is updated
- Attach the corresponding code snippet to each item of the explanation

### Format

Write it in Markdown in `tasks/<review id>/pr-explanation.md`. Order each step as "explanation → link → code snippet", so reading from top to bottom follows the order in which the code is read.

````markdown
# PR Explanation: <pull request title>

<pull request URL> · head: <commit SHA>

## TL;DR

<a few lines>

## Summary of Changes

| Module | Changes |
|--------|---------|
| pkg/foo | ... |

## Places to Look At

- ⚠️ <something that needs a decision>: <details>
- <something to check>: <details>

## Tests

- Confirmed: ...
- Not confirmed: ...
- CI: ...

## Code Walkthrough

### 1. <step heading> (pkg/foo)

<the role of this function, where it is called from, what changed>

[pkg/foo/foo.go#L40-L44](https://github.com/<owner>/<repo>/blob/<commit SHA>/pkg/foo/foo.go#L40-L44)

```diff
 func Foo() error {
-	return bar()
+	if err := bar(); err != nil {
+		return fmt.Errorf("bar: %w", err)
+	}
+	return nil
 }
```

### 2. ...
````

- Limit snippets to the range needed for the explanation. Do not paste whole files or whole hunks
- Use `diff` to show changes, and a code block in the relevant language to show new code or surrounding context

For pull requests whose diff is too long to read comfortably in Markdown, you may generate HTML with the code on the left and the explanation on the right. See `saki skill show pr-review references/pr-explanation-html.md` for the steps.
