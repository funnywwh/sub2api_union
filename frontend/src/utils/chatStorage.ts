const CHAT_STORAGE_PREFIX = 'sub2api_chat_'

function clearStorageByPrefix(storage: Storage) {
  const keysToRemove: string[] = []

  for (let i = 0; i < storage.length; i += 1) {
    const key = storage.key(i)
    if (key && key.startsWith(CHAT_STORAGE_PREFIX)) {
      keysToRemove.push(key)
    }
  }

  for (const key of keysToRemove) {
    storage.removeItem(key)
  }
}

export function clearLegacyChatLocalStorage() {
  clearStorageByPrefix(localStorage)
}

export function clearAllStoredChatData() {
  clearStorageByPrefix(localStorage)
  clearStorageByPrefix(sessionStorage)
}
