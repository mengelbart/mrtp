import { defineConfig } from 'vite'

export default ({
    build: {
        manifest: true,
        rollupOptions: {
            input: '/src/index.ts',
        },
    },
})