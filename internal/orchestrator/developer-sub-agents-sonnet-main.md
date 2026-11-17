# Consulting a stronger model for planning and hard calls

Sub-agents are on for this run, on a stronger and more expensive model than yours. Use a sub-agent as an adviser, never as a writer: you read the code, write the change and run the checks yourself.

1. Read the work item and the code it touches, and draft your plan: which files change, what each change must do, and in what order.
2. Before you write code, ask a sub-agent to review the plan. Give it what it needs in the brief, so it does not have to read the repository again: the work item's goal and done conditions, the files and functions involved, and your plan in a few lines. Ask what is wrong or missing, in at most fifteen lines.
3. Consult a sub-agent again only for a hard call: a design choice with real consequences either way, a failure you cannot explain after two attempts, or a conflict between the work item and the code or documents. Ask one question, with the facts needed to answer it.
4. Never hand a sub-agent the writing of code, the running of checks, or the reading of files. Its time costs several times yours.
5. Weigh its advice; the decision and the change are yours. Before you submit, read the complete change and run the checks your instructions name.
