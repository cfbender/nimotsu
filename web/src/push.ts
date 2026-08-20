import { PushNotifications } from '@capacitor/push-notifications'
import { registerDevice } from './api'

export async function enablePushNotifications(): Promise<void> {
  let permissions = await PushNotifications.checkPermissions()
  if (permissions.receive === 'prompt' || permissions.receive === 'prompt-with-rationale') {
    permissions = await PushNotifications.requestPermissions()
  }
  if (permissions.receive !== 'granted') {
    throw new Error('Notification permission was not granted')
  }

  await PushNotifications.createChannel({
    id: 'tracking_updates',
    name: 'Tracking updates',
    description: 'Package status changes',
    importance: 4,
    vibration: true,
  })

  const token = await new Promise<string>(async (resolve, reject) => {
    const registration = await PushNotifications.addListener('registration', (result) => {
      void registration.remove()
      void failure.remove()
      resolve(result.value)
    })
    const failure = await PushNotifications.addListener('registrationError', (error) => {
      void registration.remove()
      void failure.remove()
      reject(new Error(error.error))
    })
    try {
      await PushNotifications.register()
    } catch (error) {
      await registration.remove()
      await failure.remove()
      reject(error)
    }
  })

  await registerDevice(token)
}
