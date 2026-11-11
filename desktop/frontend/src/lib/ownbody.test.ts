import { describe, it, expect } from "vitest";
import { ownBody } from "./turn";

const head = "[acpost] Message msg_01M730WCJE0K5VZRGDS3PM0JCZ from user:lev.\nYour final answer in this turn is sent back to the sender as the reply. To ask another agent, run `acpost send --to agent:<name> --body \"…\" --wait`. To attach files to your reply, run `acpost attach FILE…`.\n\n";
const note = "[acpost] The user reads your answers in the acpost app, which renders Markdown, code blocks and Mermaid. When a flow, an architecture, a sequence of calls or a state machine is clearer as a picture, draw it in a ```mermaid block (flowchart TD/LR or sequenceDiagram; every label in double quotes, A[\"api (HTTP)\"] -->|\"calls\"| B[\"store\"]; no HTML; about 15 nodes at most). Do not draw what a sentence says as well.\n\n";
const know = "[acpost] What the user keeps as knowledge for this work (follow it unless the task says otherwise):\n\n## style\nshort\n\n[acpost] The task:\n\n";

describe("a prompt acpost sent, read back from a session, shows what the person wrote", () => {
  it("drops the node's lines before the body", () => {
    expect(ownBody(head + "/skills")).toBe("/skills");
    expect(ownBody(head + note + "/skills")).toBe("/skills");
    expect(ownBody(head + note + know + "do it\n\nnow")).toBe("do it\n\nnow");
    expect(ownBody("[acpost] Attached files:\n- a.png (image/png, 3 bytes): in this prompt\n\n" + head + "look")).toBe("look\n\n📎 a.png");
    // A screenshot alone: the message is not empty (History hides an empty one), its file is named.
    expect(ownBody("[acpost] Attached files:\n- shot (1).png (image/png, 3 bytes): in this prompt\n- notes.md (text/markdown, 9 bytes): /tmp/x/notes.md (a copy until this turn ends)\n\n" + head + note)).toBe("📎 shot (1).png\n📎 notes.md");
    expect(ownBody(head.replace("from user:lev.", "from user:lev, task t1 (attempt 2: an earlier attempt was interrupted).") + "again")).toBe("again");
  });
  it("leaves any other text as it is", () => {
    for (const s of ["", "hello", "[acpost] Message is a phrase I typed", note + "x", "see\n" + note]) expect(ownBody(s)).toBe(s);
  });
  it("takes off only the node's own lines, whole and at the start (review round 1)", () => {
    // A header without the node's lines after it, or quoted inside a message: the person's text.
    const bare = "[acpost] Message msg_01M730WCJE0K5VZRGDS3PM0JCZ from user:lev.\nYour final answer in this turn is sent back to the sender as the reply.\n\nmine";
    expect(ownBody(bare)).toBe(bare);
    const quoted = "look at this prompt:\n" + head + "what is it?";
    expect(ownBody(quoted)).toBe(quoted);
    // After a real header: a paragraph that only starts like the note, or like the knowledge, stays.
    const like = "[acpost] The user reads your answers in the acpost app is a sentence I want you to edit.\n\nrest";
    expect(ownBody(head + like)).toBe(like);
    const likeKnow = "[acpost] What the user keeps as knowledge is my question.\n\n[acpost] The task:\n\nx";
    expect(ownBody(head + likeKnow)).toBe(likeKnow);
  });
});
