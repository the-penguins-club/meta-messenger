import { stripVTControlCharacters } from 'node:util';
import stringWidth from 'string-width';

const graphemes = new Intl.Segmenter(undefined, { granularity: 'grapheme' });

/** Remote text is untrusted terminal input, including OSC hyperlinks and C0/C1 controls. */
export function safeText(value: string): string {
  return stripVTControlCharacters(value).replace(/[\x00-\x1f\x7f-\x9f]/g, ' ');
}

export function clip(value: string, width: number): string {
  if (width <= 0) return '';
  // Measure display cells and keep combining sequences/emoji intact when clipping.
  let out = '', used = 0;
  for (const { segment } of graphemes.segment(safeText(value))) {
    const cells = stringWidth(segment);
    if (used + cells > width) break;
    used += cells; out += segment;
  }
  return out + ' '.repeat(Math.max(0, width - used));
}

export function paneRow(left: string, right: string, leftWidth: number, rightWidth: number,
  selectLeft = false, selectRight = false): string {
  const cell = (text: string, width: number, selected: boolean): string => {
    const padded = clip(text, width);
    return selected ? `\x1b[7m${padded}\x1b[0m` : padded;
  };
  // Pin the divider and right pane to terminal columns. Font/script shaping can
  // disagree with Unicode width tables; it must never move the other pane.
  return `\x1b[2K${cell(left, leftWidth, selectLeft)}\x1b[${leftWidth + 2}G│\x1b[${leftWidth + 4}G${cell(right, rightWidth, selectRight)}`;
}
