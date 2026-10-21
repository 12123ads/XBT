// @vitest-environment jsdom
import { afterAll, afterEach, beforeAll, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import client from '../api/client';
import RoomCheck from './RoomCheck';

vi.mock('../api/client', () => ({
  default: {
    get: vi.fn(),
    put: vi.fn(),
    post: vi.fn(),
  },
}));

const NativeDateTimeFormat = Intl.DateTimeFormat;

// Reproduce the WebView that accepts `timeZone` but renders UTC anyway.
beforeAll(() => {
  (Intl as unknown as { DateTimeFormat: unknown }).DateTimeFormat = function (
    locales?: Intl.LocalesArgument,
    options?: Intl.DateTimeFormatOptions,
  ) {
    const requested = options?.timeZone;
    if (requested !== undefined) {
      new NativeDateTimeFormat(locales, { timeZone: requested });
    }
    return new NativeDateTimeFormat(locales, { ...options, timeZone: 'UTC' });
  };
});

afterAll(() => {
  (Intl as unknown as { DateTimeFormat: typeof Intl.DateTimeFormat }).DateTimeFormat =
    NativeDateTimeFormat;
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

const mockSettings = (settings: Record<string, unknown>) => {
  vi.mocked(client.get).mockResolvedValue({
    data: {
      code: 0,
      message: 'ok',
      data: {
        settings: {
          enabled: true,
          timezone: 'Asia/Shanghai',
          run_at: '22:00',
          next_run_window_end: 0,
          ...settings,
        },
        config: {
          user_uid: 42,
          enabled: true,
          location_name: 'dorm',
          location_index: 1,
          longitude: 120,
          latitude: 30,
          range: 100,
        },
        last_record: null,
        presets: [],
      },
    },
  } as never);
};

const renderRoomCheck = () => render(
  <MemoryRouter>
    <RoomCheck />
  </MemoryRouter>
);

describe('RoomCheck schedule display', () => {
  it('renders the server text even when the millis field is zero', async () => {
    mockSettings({
      next_run_at: 0,
      next_run_at_text: '2026/10/22 22:03:12',
    });
    renderRoomCheck();

    await waitFor(() => {
      expect(screen.getByText('2026/10/22 22:03:12')).toBeTruthy();
    });
  });

  it('falls back to fixed +08:00 arithmetic when the server sends no text', async () => {
    mockSettings({
      next_run_at: Date.parse('2026-10-22T14:03:12Z'),
      next_run_at_text: undefined,
    });
    renderRoomCheck();

    await waitFor(() => {
      expect(screen.getByText('2026/10/22 22:03:12')).toBeTruthy();
    });
  });
});
