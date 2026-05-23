# Guardian Integration for search Tool

## Overview
Add guardian policy enforcement to the `search` tool so that web search queries are subject to the guardian's allow/ask/block decisions. The `search` tool makes HTTP requests to DuckDuckGo — this is a `GuardianActionNetwork` action.

## Context
- **Tool file**: `search.go`
- **Test file**: `search_test.go`
- **Reference pattern**: `weave-bash` extension (`bash.go` lines 68-317)
- **Guardian action**: `sdk.GuardianActionNetwork`
- The search tool has **no sandbox integration**; guardian is the primary enforcement mechanism.
- Network access is a distinct action category from file reads and command execution.

## Development Approach
- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**
- Run tests after each change

## Testing Strategy
- **Unit tests**: mock guardian with allow/block/ask/error decisions
- Verify guardian check runs before HTTP request
- Verify guardian block returns error result without making request
- Verify guardian allow proceeds to search logic

## Implementation Steps

### Task 1: Add guardian infrastructure to search.go
- [x] Add `guardianMu sync.RWMutex`, `guardian sdk.Guardian` package-level variables
- [x] Add `setGuardian()` / `getGuardian()` helpers
- [x] Add `GuardianRegisteredTopic` listener in `init()` alongside tool registration
- [x] Add `newRequestID()` helper
- [x] Add `guardianRequest(query string) sdk.GuardianRequest` helper with `Action: sdk.GuardianActionNetwork`
- [x] Add `checkGuardian()` helper (same pattern as bash)
- [x] Add `formatGuardianBlock()` helper (same as bash)
- [x] Call `checkGuardian()` at start of `Execute()`, before search query execution
- [x] Run search tests — must pass before next task

### Task 2: Add guardian tests to search_test.go
- [x] Write `TestExecuteWithGuardian` with subtests:
  - "allow decision permits search"
  - "block decision returns guardian error"
  - "missing guardian permits search"
  - "guardian error returns tool error"
- [x] Add `testGuardian` mock helper
- [x] Run search tests — must pass

### Task 3: Verify and cleanup
- [ ] Run `make lint` in search extension directory
- [ ] Run full test suite for search extension
- [ ] Verify no regressions in existing search functionality

## Technical Details

### guardianRequest for search
```go
func guardianRequest(query string) sdk.GuardianRequest {
    return sdk.GuardianRequest{
        ID:          newRequestID("search-guardian"),
        ToolName:    "search",
        Action:      sdk.GuardianActionNetwork,
        Description: "Web search: " + query,
        Metadata: map[string]any{
            "operation": "search",
            "query":     query,
        },
    }
}
```

### Execute ordering
1. Validate `query` parameter
2. **Guardian check** (`checkGuardian`) — if blocked, return error
3. Parse max_results
4. Apply rate limiting cooldown
5. Query DuckDuckGo, process results

## Post-Completion
- Manual verification: test search with `ask` profile — should prompt for approval
- Test with `auto` profile — should allow normal searches, ask for unusual queries
