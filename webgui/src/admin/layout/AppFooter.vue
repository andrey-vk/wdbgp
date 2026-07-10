<script setup lang="ts">
import { useAuthStore } from '@/admin/stores/auth'

const auth = useAuthStore()
</script>

<template>
  <div class="layout-footer">
    WDBGP
    <span v-if="auth.serverVersion" class="text-muted-color">{{ auth.serverVersion }}</span>
    —
    <a
      href="https://github.com/andrey-vk/wdbgp"
      target="_blank"
      rel="noopener noreferrer"
      class="text-primary font-bold hover:underline"
    >GitHub</a>
  </div>
</template>
