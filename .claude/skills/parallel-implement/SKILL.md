# Parallel Implementation
1. Create a git worktree per sub-agent: `git worktree add ../agent-N branch-N`
2. Assign each agent a non-overlapping set of files
3. After all agents complete, merge branches sequentially with rebase
4. Run `gofmt -w . && go test ./...` after each merge
5. Only commit to main when all tests pass