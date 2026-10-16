const HEX = '0123456789ABCDEF';

export function parseHex(input: string): Uint8Array {
  const cleaned = input.replace(/[\s,;:_-]/g, '').toUpperCase();
  if (cleaned.length % 2 !== 0) {
    throw new Error('HEX 必须包含偶数个字符');
  }
  const bytes = new Uint8Array(cleaned.length / 2);
  for (let i = 0; i < cleaned.length; i += 2) {
    const high = HEX.indexOf(cleaned[i]);
    const low = HEX.indexOf(cleaned[i + 1]);
    if (high < 0 || low < 0) {
      throw new Error('请输入有效的十六进制字节');
    }
    bytes[i / 2] = (high << 4) | low;
  }
  return bytes;
}

export function formatHex(bytes: Uint8Array): string {
  const parts: string[] = [];
  for (let i = 0; i < bytes.length; i++) {
    parts.push(HEX[bytes[i] >> 4] + HEX[bytes[i] & 15]);
  }
  return parts.join(' ');
}

export function withModbusCrc(payload: Uint8Array): Uint8Array {
  let crc = 0xFFFF;
  for (let i = 0; i < payload.length; i++) {
    crc ^= payload[i];
    for (let bit = 0; bit < 8; bit++) {
      crc = (crc & 1) !== 0 ? (crc >> 1) ^ 0xA001 : crc >> 1;
    }
  }
  const result = new Uint8Array(payload.length + 2);
  result.set(payload);
  result[payload.length] = crc & 0xFF;
  result[payload.length + 1] = (crc >> 8) & 0xFF;
  return result;
}
