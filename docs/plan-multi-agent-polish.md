# Implementation Plan: Multi-Agent Polish (Shogun-Inspired)

## Concept

Inspired by [yohey-w/multi-agent-shogun](https://github.com/yohey-w/multi-agent-shogun), add an optional **review-then-refine** step after Claude's initial implementation. A second Claude invocation acts as a "Gunshi" (strategist/reviewer) that critiques the diff, then a third invocation applies the fixes. This creates a two-agent feedback loop: **Implementer → Reviewer → Refiner**.

The key insight from multi-agent-shogun is that separate agents with distinct roles (worker vs. strategist) produce better results than a single pass. We adapt this to our webhook-driven, non-interactive architecture using sequential Claude CLI calls in the same worktree.

## Trigger

- New command: `@claude approve --polish` (or `@claude approve --polish --auto-merge`)
- The `--polish` flag enables the review-refine cycle after initial implementation
- Without `--polish`, behavior is unchanged (backward compatible)

## Architecture

```
@claude approve --polish
        │
        ▼
┌─────────────────────┐
│  1. Implement        │  ← existing handleApprove logic
│     (Ashigaru role)  │
└─────────┬───────────┘
          │ git diff
          ▼
┌─────────────────────┐
│  2. Review           │  ← NEW: second Claude call (Gunshi role)
│     Critique the diff│
│     Output: findings │
└─────────┬───────────┘
          │ review text
          ▼
┌─────────────────────┐
│  3. Refine           │  ← NEW: third Claude call (Ashigaru role)
│     Apply fixes from │
│     review feedback  │
└─────────┬───────────┘
          │
          ▼
   Commit & Push & PR     ← existing logic
```

## Files to Modify

### 1. `main.go` — Core changes

#### a. Parse `--polish` flag from approve command

**Location:** `classifyComment` / `handleIssueComment` area (lines ~598-670)

- Extend the approve command parsing to detect `--polish` in the comment body
- Pass a `polish bool` parameter through to `handleApprove`

**Changes:**
```go
// In parseApproveFlags or equivalent extraction logic:
// "@claude approve --polish fix error handling"
//   → polish=true, autoMerge=false, extraGuidance="fix error handling"
```

#### b. Add `reviewTimeout` constant

**Location:** Constants block (line ~200)

```go
const reviewTimeout = 15 * time.Minute  // review is faster than implementation
```

#### c. Add `runReview` function

**New function** (~30 lines) that takes the git diff output and returns review findings.

```go
func runReview(dir string, diff string, onUpdate func(string)) (*streamResult, error)
```

**Prompt strategy** (Gunshi role):
- "You are a code reviewer. Analyze this diff for: bugs, edge cases, style violations, missing error handling, security issues."
- "Output a numbered list of concrete, actionable findings. Each finding must reference a specific file and line."
- "If the code is good, say 'LGTM' and nothing else."
- System prompt is the same non-interactive systemPrompt but with an **additional override**: "Do NOT modify any files. Only produce review text."

#### d. Add `runRefine` function

**New function** (~25 lines) that applies review feedback.

```go
func runRefine(dir string, reviewFindings string, originalPrompt string, onUpdate func(string)) (*streamResult, error)
```

**Prompt strategy** (refinement Ashigaru role):
- "A code reviewer found the following issues in your implementation. Fix ALL of them."
- Include the review findings verbatim
- Include the original task context (truncated to 2000 chars) so Claude has enough context
- Use the same `systemPrompt` (must make actual file changes)

#### e. Integrate polish cycle into `handleApprove`

**Location:** After `retryIfNoChanges` succeeds (line ~933), before `filterSafeFiles` (line ~939)

```go
if polish {
    // Step 2: Review
    updateComment(progressBody("Reviewing implementation (polish mode)", ""))
    diff, _ := runCmd(worktreeDir, gitTimeout, "git", "diff")
    reviewResult, err := runReview(worktreeDir, diff, func(partial string) {
        updateComment(progressBody("Reviewing", partial))
    })
    if err != nil {
        log.Printf("[%s#%d] review failed, proceeding without polish: %v", repo, num, err)
        // Non-fatal: proceed with unpolished implementation
    } else if !strings.Contains(strings.ToUpper(reviewResult.Text), "LGTM") {
        // Step 3: Refine
        updateComment(progressBody("Polishing based on review", ""))
        _, err := runRefine(worktreeDir, reviewResult.Text, prompt, func(partial string) {
            updateComment(progressBody("Polishing", partial))
        })
        if err != nil {
            log.Printf("[%s#%d] refine failed, proceeding with original: %v", repo, num, err)
        }
    }
}
```

**Key design decisions:**
- Polish failures are **non-fatal** — fall back to the original implementation
- If the reviewer says "LGTM", skip the refine step (avoid unnecessary work)
- The diff is captured *before* refinement starts, so the reviewer sees exactly what was implemented

#### f. Update progress comments to show polish phase

The `updateComment` calls already support this — just use different status strings:
- "Reviewing implementation (polish mode)"
- "Polishing based on review feedback"

### 2. `main_test.go` — Tests

#### a. Test `--polish` flag parsing

```go
func TestClassifyCommentPolish(t *testing.T) {
    // "@claude approve --polish" → approve with polish=true
    // "@claude approve --polish --auto-merge" → approve with both flags
    // "@claude approve --polish fix tests" → polish=true, extraGuidance="fix tests"
}
```

#### b. Test review prompt construction

```go
func TestRunReviewPrompt(t *testing.T) {
    // Verify the review prompt includes the diff
    // Verify it instructs no file modifications
    // Verify it asks for actionable findings
}
```

#### c. Test LGTM detection (skip refine)

```go
func TestPolishSkipsRefineOnLGTM(t *testing.T) {
    // If review output contains "LGTM", refine step should be skipped
}
```

#### d. Test non-fatal polish failure

```go
func TestPolishFailureNonFatal(t *testing.T) {
    // If review or refine fails, implementation should still proceed
}
```

## Edge Cases

1. **Review produces no actionable findings ("LGTM")** → Skip refine step, proceed to commit. Detect via case-insensitive check for "LGTM" in review output.

2. **Review or refine Claude call fails/times out** → Log warning, proceed with original implementation. Polish is an enhancement, not a gate.

3. **Refine step introduces new bugs or removes valid code** → This is inherent to the approach. Mitigated by:
   - Keeping the review prompt focused on concrete, specific findings
   - The refine prompt explicitly says "fix ONLY the listed issues, do not change anything else"
   - Users can always `@claude approve` without `--polish` if they prefer single-pass

4. **Very large diffs exceed prompt limits** → Truncate the diff to first 10,000 characters in the review prompt, with a note that it's truncated. Claude's context window is large but we want to leave room for analysis.

5. **Polish doubles the cost/time** → Document this clearly. Two additional Claude calls (~15min review + ~30min refine) on top of the ~60min implementation. Users opt in explicitly via `--polish`.

6. **Concurrent polish runs hitting semaphore** → No change needed. The existing semaphore already limits concurrent Claude runs. Polish runs are sequential within a single issue handler, so they share the same semaphore slot.

7. **`--polish` combined with `--auto-merge`** → Both flags should work together. The polish cycle runs before commit/push, so auto-merge happens on the polished result.

## Testing Approach

### Unit Tests (in `main_test.go`)

1. **Flag parsing**: Verify `--polish` is correctly extracted from various comment formats
2. **Review prompt construction**: Ensure diff is embedded, no-modify instruction is present
3. **Refine prompt construction**: Ensure review findings are embedded, original context included
4. **LGTM detection**: Various casing ("LGTM", "lgtm", "Lgtm", "The code looks good, LGTM")
5. **Error tolerance**: Review/refine errors don't propagate to the main flow

### Integration Testing (manual)

1. Create a test issue, `@claude approve --polish` → verify three Claude calls happen, PR includes polished code
2. Create a trivial issue where review should say LGTM → verify refine is skipped
3. Test `@claude approve --polish --auto-merge` → verify both features work together
4. Test with `@claude approve` (no polish) → verify no regression

## Cost & Performance Impact

| Phase | Timeout | Estimated Cost | When |
|-------|---------|----------------|------|
| Implement | 60min | ~$0.50-2.00 | Always |
| Review | 15min | ~$0.10-0.30 | Only with `--polish` |
| Refine | 30min | ~$0.20-1.00 | Only if review finds issues |

**Worst case with `--polish`**: ~3x the cost and ~2x the time of a normal run.
**Best case with `--polish`**: ~1.2x cost (review says LGTM, no refine needed).

## Future Extensions (Not in This PR)

- **`@claude approve --polish=2`**: Multiple review-refine iterations (configurable loop count)
- **Automatic polish for complex issues**: Heuristic based on issue length/complexity to auto-enable polish
- **Specialized reviewer prompts**: Different review focuses (security, performance, style) inspired by Shogun's role-specific agents
- **PR-level polish**: Apply the same review-refine cycle to `handlePRComment`
