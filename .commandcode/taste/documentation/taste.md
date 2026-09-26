# Taste — Documentation

- Wants complete, detailed technical documentation covering what the project contains, how to extend it, and how to use it — written to be easily understood by both humans and LLM agents. Confidence: 0.85
- Separates technical docs from installation/usage manuals: prefers capability- and integration-focused docs over step-by-step install walkthroughs. Confidence: 0.75
- Expects all relevant docs (README, PRD, plan, technical docs) to be updated whenever features or build targets change, and kept free of drift — stale references to deleted/renamed symbols or claims that no longer match the code are treated as defects. Confidence: 0.85
- Wants the testing strategy (unit, integration, etc.) written down in the .md plan. Confidence: 0.7
- Expects API documentation to ship with a ready-to-import Postman collection (collection + environment JSON) alongside the written endpoint reference. Confidence: 0.55
- When a new feature expands scope, wants a **separate, feature-specific plan document** (e.g. `PLAN-DASHBOARD.md` at the repo root) rather than folding it into the main PLAN.md, cross-linked with the PRD section that describes the feature. Confidence: 0.75
- Expects the PRD to be explicitly revised whenever scope shifts — goals/non-goals/roadmap lines updated to move the item into scope (or keep it out) with a note — instead of silently adding a feature that the PRD still lists as a non-goal. Confidence: 0.7
- Pushes for ever-greater depth after a deliverable is already done — asks to expand completed docs to be "more detailed" (tables of fields/status codes, full request+response examples, edge cases, FAQ) rather than accepting a concise version. Confidence: 0.7
