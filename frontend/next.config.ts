import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  /**
   * Use Turbopack (Next.js 16 default bundler).
   * Empty config silences the webpack migration warning.
   * WATCHPACK_POLLING env var handles hot-reload in Docker.
   */
  turbopack: {},
};

export default nextConfig;
