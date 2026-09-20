"use client";

import { useEffect } from "react";

declare global {
  interface Window {
    Telegram?: {
      WebApp?: {
        ready: () => void;
        expand: () => void;
        close: () => void;
        setHeaderColor?: (color: string) => void;
        setBackgroundColor?: (color: string) => void;
        initDataUnsafe?: {
          user?: {
            id: number;
            first_name: string;
            last_name?: string;
            username?: string;
          };
        };
      };
    };
  }
}

export function TelegramWebAppInit() {
  useEffect(() => {
    if (typeof window !== "undefined" && window.Telegram?.WebApp) {
      try {
        const tg = window.Telegram.WebApp;
        tg.ready();
        tg.expand();
        if (tg.setHeaderColor) {
          tg.setHeaderColor("#030712"); // gray-950
        }
        if (tg.setBackgroundColor) {
          tg.setBackgroundColor("#030712");
        }
      } catch (err) {
        console.error("Telegram WebApp initialization failed:", err);
      }
    }
  }, []);

  return null;
}
