import { describe, it, expect } from "vitest";
import { ownBody, readerNote } from "./turn";

const head = "[acpost] Message msg_01M730WCJE0K5VZRGDS3PM0JCZ from user:lev.\nYour final answer in this turn is sent back to the sender as the reply. To ask another agent, run `acpost send --to agent:<name> --body \"…\" --wait`. To attach files to your reply, run `acpost attach FILE…`.\n\n";
// The note sessions were given before the app drew components (ownbody.test.ts has it too).
const before = "[acpost] The user reads your answers in the acpost app, which renders Markdown, code blocks and Mermaid. When a flow, an architecture, a sequence of calls or a state machine is clearer as a picture, draw it in a ```mermaid block (flowchart TD/LR or sequenceDiagram; every label in double quotes, A[\"api (HTTP)\"] -->|\"calls\"| B[\"store\"]; no HTML; about 15 nodes at most). Do not draw what a sentence says as well.\n\n";
const know = "[acpost] What the user keeps as knowledge for this work (follow it unless the task says otherwise):\n\n## style\nshort\n\n[acpost] The task:\n\n";

describe("the reader's note with components, taken off a prompt read back", () => {
  it("names the ui block and its components", () => {
    expect(readerNote).toContain("```ui block");
    for (const c of ["stats", "chart", "table", "timeline", "kanban", "steps", "choices", "form"]) expect(readerNote).toContain(c + " {");
    expect(readerNote.length - before.length).toBeLessThan(1000);
  });
  it("is taken off, and so is the note of sessions started before", () => {
    expect(ownBody(head + readerNote + "/skills")).toBe("/skills");
    expect(ownBody(head + readerNote + know + "do it")).toBe("do it");
    expect(ownBody(head + before + know + "do it")).toBe("do it");
  });
  it("is taken off once, whole, after the header only", () => {
    expect(ownBody(head + readerNote + readerNote + "x")).toBe(readerNote + "x");
    expect(ownBody(readerNote + "x")).toBe(readerNote + "x");
    const cut = readerNote.slice(0, 200) + "\n\nmine";
    expect(ownBody(head + cut)).toBe(cut);
  });
});
