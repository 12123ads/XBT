export interface QrData {
  enc: string;
  c: string;
  timestamp: number;
}

export const parseChaoxingQrText = (text: string): QrData | null => {
  if (!text.includes('mobilelearn.chaoxing.com')) return null;
  try {
    const url = new URL(text);
    const enc = url.searchParams.get('enc');
    const c = url.searchParams.get('c');
    if (enc) return { enc, c: c || '', timestamp: Date.now() };
  } catch {
    const encMatch = text.match(/[?&]enc=([^&]+)/);
    const cMatch = text.match(/[?&]c=([^&]+)/);
    if (encMatch) return { enc: encMatch[1], c: cMatch ? cMatch[1] : '', timestamp: Date.now() };
  }
  return null;
};
