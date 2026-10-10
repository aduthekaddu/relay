---
name: verify
description: Prove a relay change works by driving the real app, using the verify-relay recipe for the feature the change touches. Use before committing a change to relay behaviour.
---

# verify

A thin pointer for Claude Code, which runs a project skill named `verify` before each commit (Claude Code 2.1.286 or later). The real instructions live in `verify-relay`, which every tool reads.

1. Read `.agents/skills/verify-relay/SKILL.md` and follow it for the feature this change touches.
2. If the change only touches docs or tests, say so and skip the drive.
3. Report the verdict (VERIFIED, NOT VERIFIED or INCONCLUSIVE) and the `.proof/` bundle path.

Do not copy instructions into this file. Edit `verify-relay` instead.
