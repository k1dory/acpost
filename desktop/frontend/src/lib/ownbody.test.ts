import { describe, it, expect } from "vitest";
import { ownBody, attachedName } from "./turn";

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
  it("drops what the chat's other agents said, told before the message of a shared chat", () => {
    const shared = "[acpost] This chat is shared with other agents: you see only your own turns of it. Since your last turn here (2 earlier exchanges are left out):\n\n" +
      "[user:lev asked agent:codex]\nreview\n[agent:codex answered]\nfine\n[acpost] That is what the others said. The message to you:\n\nquoted\n\n" +
      "[acpost] That is what the others said. The message to you:\n\n";
    expect(ownBody(head + shared + "now do it")).toBe("now do it");
    expect(ownBody(head + note + know + shared + "now do it")).toBe("now do it");
    const typed = "[acpost] This chat is shared with other agents is what I typed";
    expect(ownBody(head + typed)).toBe(typed);
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
  it("names a file whatever brackets its name or its type has (review round 3)", () => {
    expect(attachedName("- a.png (image/png, 3 bytes): in this prompt")).toBe("a.png");
    expect(attachedName('- shot (1).png (image/png; name="shot (1).png", 3 bytes): in this prompt')).toBe("shot (1).png");
    expect(attachedName("- shot (1): fixed.png (image/png, 9 bytes): /tmp/x/shot (1): fixed.png (a copy until this turn ends)")).toBe("shot (1): fixed.png");
    expect(attachedName("- spec (application/pdf): a link, https://example.com/a (b).pdf")).toBe("spec");
    expect(attachedName('- report.txt (text/plain; comment="rendered as (text/html; charset=utf-8)", 5 bytes): in this prompt')).toBe("report.txt");
    // A line not read: whole, not dropped.
    expect(attachedName("- odd line without a type")).toBe("odd line without a type");
    const only = '[acpost] Attached files:\n- shot (1).png (image/png; name="shot (1).png", 3 bytes): in this prompt\n\n' + head;
    expect(ownBody(only)).toBe("📎 shot (1).png");
  });
});
