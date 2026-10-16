import test from 'node:test';
import assert from 'node:assert/strict';
import { formatHex, parseHex, withModbusCrc } from '../entry/src/main/ets/common/PacketCodec.ts';

test('HEX input round trips raw bytes', () => {
  const bytes = parseHex('01 03 00 00 00 01');
  assert.deepEqual([...bytes], [1, 3, 0, 0, 0, 1]);
  assert.equal(formatHex(bytes), '01 03 00 00 00 01');
});

test('HEX input rejects malformed bytes', () => {
  assert.throws(() => parseHex('123'), /偶数/);
  assert.throws(() => parseHex('01 GG'), /十六进制/);
});

test('Modbus RTU CRC appends low byte first', () => {
  assert.equal(formatHex(withModbusCrc(parseHex('01 03 00 00 00 01'))), '01 03 00 00 00 01 84 0A');
});
