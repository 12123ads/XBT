const VERSION = 4;
const SIZE = 17 + VERSION * 4;
const DATA_CODEWORDS = 80;
const EC_CODEWORDS = 20;
const FORMAT_MASK = 0x5412;
const FORMAT_GENERATOR = 0x537;

type Matrix = Array<Array<boolean | null>>;
type Reserved = boolean[][];

const alignmentPositions = [6, 26];

const gfExp = new Array<number>(512).fill(0);
const gfLog = new Array<number>(256).fill(0);

let x = 1;
for (let i = 0; i < 255; i += 1) {
  gfExp[i] = x;
  gfLog[x] = i;
  x <<= 1;
  if (x & 0x100) x ^= 0x11d;
}
for (let i = 255; i < 512; i += 1) gfExp[i] = gfExp[i - 255];

const gfMul = (a: number, b: number) => {
  if (a === 0 || b === 0) return 0;
  return gfExp[gfLog[a] + gfLog[b]];
};

const rsGenerator = (degree: number) => {
  let poly = [1];
  for (let i = 0; i < degree; i += 1) {
    const next = new Array<number>(poly.length + 1).fill(0);
    for (let j = 0; j < poly.length; j += 1) {
      next[j] ^= gfMul(poly[j], 1);
      next[j + 1] ^= gfMul(poly[j], gfExp[i]);
    }
    poly = next;
  }
  return poly;
};

const rsRemainder = (data: number[], degree: number) => {
  const generator = rsGenerator(degree);
  const result = new Array<number>(degree).fill(0);
  for (const value of data) {
    const factor = value ^ result.shift()!;
    result.push(0);
    if (factor === 0) continue;
    for (let i = 0; i < degree; i += 1) {
      result[i] ^= gfMul(generator[i + 1], factor);
    }
  }
  return result;
};

const appendBits = (bits: number[], value: number, length: number) => {
  for (let i = length - 1; i >= 0; i -= 1) {
    bits.push((value >>> i) & 1);
  }
};

const createCodewords = (text: string) => {
  const bytes = Array.from(new TextEncoder().encode(text));
  if (bytes.length > 78) {
    throw new Error('二维码内容过长');
  }

  const bits: number[] = [];
  appendBits(bits, 0b0100, 4);
  appendBits(bits, bytes.length, 8);
  bytes.forEach((byte) => appendBits(bits, byte, 8));
  appendBits(bits, 0, Math.min(4, DATA_CODEWORDS * 8 - bits.length));
  while (bits.length % 8 !== 0) bits.push(0);

  const data: number[] = [];
  for (let i = 0; i < bits.length; i += 8) {
    let value = 0;
    for (let j = 0; j < 8; j += 1) value = (value << 1) | bits[i + j];
    data.push(value);
  }
  for (let pad = 0xec; data.length < DATA_CODEWORDS; pad = pad === 0xec ? 0x11 : 0xec) {
    data.push(pad);
  }
  return [...data, ...rsRemainder(data, EC_CODEWORDS)];
};

const createEmptyMatrix = () => {
  const matrix: Matrix = Array.from({ length: SIZE }, () => Array<boolean | null>(SIZE).fill(null));
  const reserved: Reserved = Array.from({ length: SIZE }, () => Array<boolean>(SIZE).fill(false));
  return { matrix, reserved };
};

const setModule = (matrix: Matrix, reserved: Reserved, xPos: number, yPos: number, value: boolean, reserve = true) => {
  if (xPos < 0 || yPos < 0 || xPos >= SIZE || yPos >= SIZE) return;
  matrix[yPos][xPos] = value;
  if (reserve) reserved[yPos][xPos] = true;
};

const drawFinder = (matrix: Matrix, reserved: Reserved, xPos: number, yPos: number) => {
  for (let y = -1; y <= 7; y += 1) {
    for (let x = -1; x <= 7; x += 1) {
      const xx = xPos + x;
      const yy = yPos + y;
      const dark = x >= 0 && x <= 6 && y >= 0 && y <= 6 && (x === 0 || x === 6 || y === 0 || y === 6 || (x >= 2 && x <= 4 && y >= 2 && y <= 4));
      setModule(matrix, reserved, xx, yy, dark);
    }
  }
};

const drawAlignment = (matrix: Matrix, reserved: Reserved, centerX: number, centerY: number) => {
  for (let y = -2; y <= 2; y += 1) {
    for (let x = -2; x <= 2; x += 1) {
      const dark = Math.max(Math.abs(x), Math.abs(y)) !== 1;
      setModule(matrix, reserved, centerX + x, centerY + y, dark);
    }
  }
};

const drawFunctionPatterns = (matrix: Matrix, reserved: Reserved) => {
  drawFinder(matrix, reserved, 0, 0);
  drawFinder(matrix, reserved, SIZE - 7, 0);
  drawFinder(matrix, reserved, 0, SIZE - 7);

  for (let i = 8; i < SIZE - 8; i += 1) {
    setModule(matrix, reserved, i, 6, i % 2 === 0);
    setModule(matrix, reserved, 6, i, i % 2 === 0);
  }

  for (const y of alignmentPositions) {
    for (const x of alignmentPositions) {
      if (reserved[y][x]) continue;
      drawAlignment(matrix, reserved, x, y);
    }
  }

  setModule(matrix, reserved, 8, SIZE - 8, true);
  for (let i = 0; i < 9; i += 1) {
    if (i !== 6) {
      setModule(matrix, reserved, 8, i, false);
      setModule(matrix, reserved, i, 8, false);
    }
  }
  for (let i = 0; i < 8; i += 1) {
    setModule(matrix, reserved, SIZE - 1 - i, 8, false);
    setModule(matrix, reserved, 8, SIZE - 1 - i, false);
  }
};

const maskValue = (mask: number, xPos: number, yPos: number) => {
  switch (mask) {
    case 0: return (xPos + yPos) % 2 === 0;
    case 1: return yPos % 2 === 0;
    case 2: return xPos % 3 === 0;
    case 3: return (xPos + yPos) % 3 === 0;
    case 4: return (Math.floor(yPos / 2) + Math.floor(xPos / 3)) % 2 === 0;
    case 5: return ((xPos * yPos) % 2) + ((xPos * yPos) % 3) === 0;
    case 6: return (((xPos * yPos) % 2) + ((xPos * yPos) % 3)) % 2 === 0;
    default: return (((xPos + yPos) % 2) + ((xPos * yPos) % 3)) % 2 === 0;
  }
};

const drawCodewords = (matrix: Matrix, reserved: Reserved, codewords: number[], mask: number) => {
  const bits: number[] = [];
  codewords.forEach((codeword) => appendBits(bits, codeword, 8));
  for (let i = 0; i < 7; i += 1) bits.push(0);

  let bitIndex = 0;
  let upward = true;
  for (let xPos = SIZE - 1; xPos > 0; xPos -= 2) {
    if (xPos === 6) xPos -= 1;
    for (let step = 0; step < SIZE; step += 1) {
      const yPos = upward ? SIZE - 1 - step : step;
      for (let dx = 0; dx < 2; dx += 1) {
        const xx = xPos - dx;
        if (reserved[yPos][xx]) continue;
        const raw = bits[bitIndex] === 1;
        matrix[yPos][xx] = raw !== maskValue(mask, xx, yPos);
        bitIndex += 1;
      }
    }
    upward = !upward;
  }
};

const formatBits = (mask: number) => {
  let data = (0b01 << 3) | mask;
  let value = data << 10;
  for (let i = 14; i >= 10; i -= 1) {
    if (((value >>> i) & 1) !== 0) value ^= FORMAT_GENERATOR << (i - 10);
  }
  return ((data << 10) | value) ^ FORMAT_MASK;
};

const drawFormat = (matrix: Matrix, reserved: Reserved, mask: number) => {
  const bits = formatBits(mask);
  for (let i = 0; i < 15; i += 1) {
    const dark = ((bits >>> i) & 1) === 1;
    if (i < 6) setModule(matrix, reserved, 8, i, dark);
    else if (i === 6) setModule(matrix, reserved, 8, 7, dark);
    else if (i === 7) setModule(matrix, reserved, 8, 8, dark);
    else if (i === 8) setModule(matrix, reserved, 7, 8, dark);
    else setModule(matrix, reserved, 14 - i, 8, dark);

    if (i < 8) setModule(matrix, reserved, SIZE - 1 - i, 8, dark);
    else setModule(matrix, reserved, 8, SIZE - 15 + i, dark);
  }
  setModule(matrix, reserved, 8, SIZE - 8, true);
};

const penalty = (matrix: Matrix) => {
  let score = 0;
  const darkCount = matrix.flat().filter(Boolean).length;

  for (let y = 0; y < SIZE; y += 1) {
    let runColor = matrix[y][0];
    let run = 1;
    for (let x = 1; x < SIZE; x += 1) {
      if (matrix[y][x] === runColor) run += 1;
      else {
        if (run >= 5) score += 3 + run - 5;
        runColor = matrix[y][x];
        run = 1;
      }
    }
    if (run >= 5) score += 3 + run - 5;
  }

  for (let x = 0; x < SIZE; x += 1) {
    let runColor = matrix[0][x];
    let run = 1;
    for (let y = 1; y < SIZE; y += 1) {
      if (matrix[y][x] === runColor) run += 1;
      else {
        if (run >= 5) score += 3 + run - 5;
        runColor = matrix[y][x];
        run = 1;
      }
    }
    if (run >= 5) score += 3 + run - 5;
  }

  for (let y = 0; y < SIZE - 1; y += 1) {
    for (let x = 0; x < SIZE - 1; x += 1) {
      const color = matrix[y][x];
      if (matrix[y][x + 1] === color && matrix[y + 1][x] === color && matrix[y + 1][x + 1] === color) score += 3;
    }
  }

  const ratio = (darkCount * 100) / (SIZE * SIZE);
  score += Math.floor(Math.abs(ratio - 50) / 5) * 10;
  return score;
};

const buildMatrix = (text: string) => {
  const codewords = createCodewords(text);
  let best: Matrix | null = null;
  let bestScore = Number.MAX_SAFE_INTEGER;

  for (let mask = 0; mask < 8; mask += 1) {
    const { matrix, reserved } = createEmptyMatrix();
    drawFunctionPatterns(matrix, reserved);
    drawCodewords(matrix, reserved, codewords, mask);
    drawFormat(matrix, reserved, mask);
    const score = penalty(matrix);
    if (score < bestScore) {
      best = matrix;
      bestScore = score;
    }
  }
  return best!;
};

export const createQRCodeDataURL = (text: string) => {
  const matrix = buildMatrix(text);
  const quiet = 4;
  const size = SIZE + quiet * 2;
  const modules = matrix.flatMap((row, y) => row.map((dark, x) => (
    dark ? `<rect x="${x + quiet}" y="${y + quiet}" width="1" height="1"/>` : ''
  ))).join('');
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${size} ${size}" shape-rendering="crispEdges"><rect width="${size}" height="${size}" fill="#fff"/><g fill="#0f172a">${modules}</g></svg>`;
  return `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`;
};
