import { randomBytes } from 'node:crypto'
import { chmodSync, existsSync, readFileSync, writeFileSync } from 'node:fs'
import { mkdir } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { spawnSync } from 'node:child_process'

const storePasswordEnv = 'NIMOTSU_KEYTOOL_STOREPASS'
const keyPasswordEnv = 'NIMOTSU_KEYTOOL_KEYPASS'

const keystorePath = resolve(
  process.env.NIMOTSU_ANDROID_KEYSTORE || 'android/release/nimotsu-release.jks',
)
const alias = process.env.NIMOTSU_ANDROID_KEY_ALIAS || 'nimotsu'
const storePassword = process.env.NIMOTSU_ANDROID_KEYSTORE_PASSWORD || randomSecret()
const keyPassword = process.env.NIMOTSU_ANDROID_KEY_PASSWORD || storePassword
const force = process.argv.includes('--force')

function randomSecret() {
  return randomBytes(24).toString('base64url')
}

function run(command, args, extraEnv) {
  const result = spawnSync(command, args, {
    encoding: 'utf8',
    env: extraEnv ? { ...process.env, ...extraEnv } : process.env,
  })
  if (result.status === 0) return result.stdout

  const detail = [result.stdout, result.stderr].filter(Boolean).join('\n')
  throw new Error(`${command} failed${detail ? `:\n${detail}` : ''}`)
}

if (existsSync(keystorePath) && !force) {
  throw new Error(
    `${keystorePath} already exists. Re-run with --force only if you intend to replace the release signing key.`,
  )
}

await mkdir(dirname(keystorePath), { recursive: true })

run(
  'keytool',
  [
    '-genkeypair',
    '-v',
    '-storetype',
    'JKS',
    '-keystore',
    keystorePath,
    '-alias',
    alias,
    '-keyalg',
    'RSA',
    '-keysize',
    '4096',
    '-validity',
    '10000',
    '-storepass:env',
    storePasswordEnv,
    '-keypass:env',
    keyPasswordEnv,
    '-dname',
    'CN=Nimotsu, OU=Nimotsu, O=Nimotsu, L=Local, ST=Local, C=US',
  ],
  { [storePasswordEnv]: storePassword, [keyPasswordEnv]: keyPassword },
)

const certificate = run(
  'keytool',
  ['-list', '-v', '-keystore', keystorePath, '-alias', alias, '-storepass:env', storePasswordEnv],
  { [storePasswordEnv]: storePassword },
)
const fingerprint = certificate.match(/SHA256:\s*([^\n]+)/)?.[1]?.trim()

if (!fingerprint) throw new Error('Could not read the generated certificate fingerprint.')

const secretsPath = resolve(dirname(keystorePath), 'github-actions-secrets.env')
const secretsFile = [
  `NIMOTSU_ANDROID_KEYSTORE_BASE64=${readFileSync(keystorePath).toString('base64')}`,
  `NIMOTSU_ANDROID_KEYSTORE_PASSWORD=${storePassword}`,
  `NIMOTSU_ANDROID_KEY_ALIAS=${alias}`,
  `NIMOTSU_ANDROID_KEY_PASSWORD=${keyPassword}`,
].join('\n') + '\n'

writeFileSync(secretsPath, secretsFile, { mode: 0o600 })
chmodSync(secretsPath, 0o600)

console.log(`Created Android release keystore: ${keystorePath}`)
console.log(`Wrote GitHub Actions secrets (mode 0600) to: ${secretsPath}`)
console.log('')
console.log('Upload the secrets, then delete the dotenv file:')
console.log(`  gh secret set -f ${secretsPath}`)
console.log(`  rm ${secretsPath}`)
console.log('')
console.log(`Signing certificate SHA-256 fingerprint: ${fingerprint}`)
console.log('Keep the .jks file and passwords in a durable backup; losing them prevents signed app updates.')
