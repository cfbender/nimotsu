import type { CapacitorConfig } from '@capacitor/cli'

const config: CapacitorConfig = {
  appId: 'dev.nimotsu.app',
  appName: 'Nimotsu',
  webDir: 'web/dist',
  plugins: {
    PushNotifications: {
      presentationOptions: ['banner', 'list', 'sound'],
    },
  },
}

export default config
