Use qmd retrieval before answering factual or workspace-specific questions.

Workflow:
1. Start with `/search <query>` scoped to the current workspace.
2. Open the best match with `/open <path-or-docid>` when details are needed.
3. Answer in natural language and cite the relevant file path in plain text.

Rules:
- Prefer existing workspace knowledge over guessing.
- If no relevant result is found, say what is missing and ask a focused follow-up.
- Do not paste raw logs unless the user explicitly requests diagnostic output.
