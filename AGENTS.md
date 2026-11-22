# Working in this lab

Read the assigned GitHub issue directly. Use its **Why**, **What**, **Constraints**, and **Validation** as the acceptance contract.

1. Run `coding_agent` on a dedicated issue branch. Pass the issue URL and exact candidate head to `code_review_agent` in a fresh context.
2. The coding agent evaluates the independent findings and records **Fix**, **Defer**, or **Disagree** with evidence. Apply minimal accepted fixes and rerun affected checks.
3. Give the reviewer the issue, revised head, and finding decisions for one independent re-review. Hand the verdict and remaining findings to the human before merge.

See docs/agent-workflow.md for the workflow graph and finding categories. Use a fresh Codex chat in the trusted project if the current chat cannot provide a fresh agent context. The reviewer keeps files and remote state read-only by instruction.

Run `make test` for Go changes. For Compose, PostgreSQL, or Envoy changes, run `make validate-envoy` and `make smoke`. Report skipped or blocked checks with the reason.
