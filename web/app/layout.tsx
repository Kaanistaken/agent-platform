import "./globals.css";
import type { ReactNode } from "react";
import Nav from "@/components/Nav";

export const metadata = { title: "Agent Platform" };

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="en">
      <body>
        <Nav />
        <main>{children}</main>
      </body>
    </html>
  );
}
