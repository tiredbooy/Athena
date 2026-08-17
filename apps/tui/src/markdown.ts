export type MarkdownStyle = "text" | "heading" | "list" | "code" | "codeFence" | "bold" | "marker";

export type MarkdownSpan = { start: number; end: number; style: MarkdownStyle };

export function markdownSpans(source: string): MarkdownSpan[] {
  const styles: MarkdownStyle[] = Array.from({ length: source.length }, () => "text");
  let index = 0;
  while (index < source.length) {
    const lineEnd = lineLimit(source, index);
    const line = source.slice(index, lineEnd);
    if (line.startsWith("```")) {
      paint(styles, index, lineEnd, "codeFence");
      index = skipNewline(source, lineEnd);
      while (index < source.length) {
        const closeEnd = lineLimit(source, index);
        const close = source.slice(index, closeEnd);
        if (close.startsWith("```")) {
          paint(styles, index, closeEnd, "codeFence");
          index = skipNewline(source, closeEnd);
          break;
        }
        paint(styles, index, closeEnd, "code");
        index = skipNewline(source, closeEnd);
      }
      continue;
    }
    const heading = /^(#{1,6} )/.exec(line);
    if (heading) {
      paint(styles, index, index + heading[1].length, "marker");
      paint(styles, index + heading[1].length, lineEnd, "heading");
      index = skipNewline(source, lineEnd);
      continue;
    }
    const list = /^([-*] |\d+\. )/.exec(line);
    if (list) {
      paint(styles, index, index + list[1].length, "list");
      paintInline(styles, source, index + list[1].length, lineEnd);
      index = skipNewline(source, lineEnd);
      continue;
    }
    paintInline(styles, source, index, lineEnd);
    index = skipNewline(source, lineEnd);
  }
  return coalesce(styles);
}

export function lineSpans(spans: MarkdownSpan[], start: number, length: number): MarkdownSpan[] {
  const end = start + length;
  const clipped: MarkdownSpan[] = [];
  for (const span of spans) {
    const from = Math.max(span.start, start);
    const to = Math.min(span.end, end);
    if (to <= from) continue;
    clipped.push({ start: from - start, end: to - start, style: span.style });
  }
  return clipped;
}

function paintInline(styles: MarkdownStyle[], source: string, start: number, end: number): void {
  let index = start;
  while (index < end) {
    if (source[index] === "`") {
      const close = source.indexOf("`", index + 1);
      if (close !== -1 && close < end) {
        paint(styles, index, index + 1, "marker");
        paint(styles, index + 1, close, "code");
        paint(styles, close, close + 1, "marker");
        index = close + 1;
        continue;
      }
    }
    if (source.startsWith("**", index)) {
      const close = source.indexOf("**", index + 2);
      if (close !== -1 && close < end) {
        paint(styles, index, index + 2, "marker");
        paint(styles, index + 2, close, "bold");
        paint(styles, close, close + 2, "marker");
        index = close + 2;
        continue;
      }
    }
    index += 1;
  }
}

function paint(styles: MarkdownStyle[], start: number, end: number, style: MarkdownStyle): void {
  const to = Math.min(styles.length, end);
  for (let index = Math.max(0, start); index < to; index++) styles[index] = style;
}

function coalesce(styles: MarkdownStyle[]): MarkdownSpan[] {
  const spans: MarkdownSpan[] = [];
  let index = 0;
  while (index < styles.length) {
    let end = index + 1;
    while (end < styles.length && styles[end] === styles[index]) end += 1;
    spans.push({ start: index, end, style: styles[index] });
    index = end;
  }
  return spans;
}

function lineLimit(source: string, start: number): number {
  const newline = source.indexOf("\n", start);
  return newline === -1 ? source.length : newline;
}

function skipNewline(source: string, lineEnd: number): number {
  return lineEnd < source.length && source[lineEnd] === "\n" ? lineEnd + 1 : lineEnd;
}
