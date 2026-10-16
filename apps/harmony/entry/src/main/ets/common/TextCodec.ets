import { util } from '@kit.ArkTS';

export type TextEncoding = 'UTF-8' | 'GBK';

export function encodeText(text: string, encoding: TextEncoding): Uint8Array {
  return util.TextEncoder.create(encoding).encodeInto(text);
}

export function decodeText(bytes: Uint8Array, encoding: TextEncoding): string {
  return util.TextDecoder.create(encoding).decodeToString(bytes);
}
