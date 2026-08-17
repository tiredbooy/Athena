import assert from "node:assert/strict";
import test from "node:test";
import { lineSpans, markdownSpans } from "./markdown.js";

test("headings, lists, and fenced code keep source offsets", () => {
  const source = "# Title\n- item\n\n```\ncode\n```\nplain **bold** and `x`";
  const spans = markdownSpans(source);
  const styleAt = (offset: number) => spans.find((span) => offset >= span.start && offset < span.end)?.style;
  assert.equal(styleAt(0), "marker");
  assert.equal(styleAt(2), "heading");
  assert.equal(styleAt(source.indexOf("- ")), "list");
  assert.equal(styleAt(source.indexOf("code")), "code");
  assert.equal(styleAt(source.indexOf("```")), "codeFence");
  assert.equal(styleAt(source.indexOf("bold")), "bold");
  assert.equal(styleAt(source.indexOf("x")), "code");
  const headingLine = lineSpans(spans, 0, "# Title".length);
  assert.deepEqual(headingLine.map((span) => source.slice(span.start, span.end)), ["# ", "Title"]);
});
