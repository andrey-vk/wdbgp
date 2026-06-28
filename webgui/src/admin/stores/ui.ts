import { defineStore } from 'pinia'
import { ref } from 'vue'

export const useUIStore = defineStore('ui', () => {
  const lastSelectedUserId = ref<number | null>(null)

  function setSelectedUser(id: number) {
    lastSelectedUserId.value = id
  }

  function clearSelectedUser() {
    lastSelectedUserId.value = null
  }

  return { lastSelectedUserId, setSelectedUser, clearSelectedUser }
})
