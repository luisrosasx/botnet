import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "BOT.NET",
  description:
    "Red de proxies residenciales sobre dispositivos abiertos, modelo backconnect.",
};

export default function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="es">
      <body>{children}</body>
    </html>
  );
}
