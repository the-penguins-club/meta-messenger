/** Parse a local JSON cookie export or Cookie header. Never executes cURL/shell input. */
export function parseCookies(input: string): Record<string, string> {
  const text = input.trim();
  const result: Record<string, string> = Object.create(null) as Record<string, string>;
  if (text.startsWith('{') || text.startsWith('[')) {
    const value: unknown = JSON.parse(text);
    if (Array.isArray(value)) {
      for (const cookie of value) {
        if (cookie && typeof cookie.name === 'string' && typeof cookie.value === 'string'
          && (!cookie.domain || /(^|\.)facebook\.com$/.test(cookie.domain))) result[cookie.name] = cookie.value;
      }
    } else if (value && typeof value === 'object') {
      for (const [key, value2] of Object.entries(value)) {
        if (typeof value2 !== 'string') throw new Error('Cookie JSON must map cookie names to string values.');
        result[key] = value2;
      }
    }
  } else {
    for (const pair of text.replace(/^cookie:\s*/i, '').split(';')) {
      const equal = pair.indexOf('=');
      if (equal > 0) result[pair.slice(0, equal).trim()] = pair.slice(equal + 1).trim();
    }
  }
  if (!result.xs || !result.c_user || !result.datr || !/^[1-9]\d*$/.test(result.c_user)) {
    throw new Error('Expected Facebook cookies xs, c_user, and datr. Use a JSON cookie export or a Cookie header file.');
  }
  return result;
}
