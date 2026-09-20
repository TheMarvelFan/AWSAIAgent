import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    port: 5173,
    // Fail loudly on a port collision instead of moving to 5174. A silent move
    // breaks every request on CORS, and the browser gives no useful explanation
    // because it fails before our code runs (§1.4, §10.1).
    strictPort: true,
  },
});
