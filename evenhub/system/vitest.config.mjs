import { defineConfig } from 'vitest/config'

export default defineConfig({
  test: {
    include: ['system/*.system.mjs'],
    environment: 'node',
    testTimeout: 30000,
    hookTimeout: 30000,
    fileParallelism: false,
    reporters: ['default', 'junit', 'json'],
    outputFile: {
      junit: `${process.env.GLASS_ARTIFACT_DIR}/host-storage-contract.xml`,
      json: `${process.env.GLASS_ARTIFACT_DIR}/host-storage-contract.json`,
    },
  },
})
