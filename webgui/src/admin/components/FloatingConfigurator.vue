<script setup lang="ts">
// @ts-nocheck
import AppConfigurator from '@/admin/layout/AppConfigurator.vue';
import { useLayout } from '@/admin/layout/composables/layout';

const { toggleDarkMode, isDarkTheme } = useLayout();
</script>

<template>
  <div class="fixed flex gap-4 top-8 right-8">
    <Button
      type="button"
      rounded
      :icon="isDarkTheme ? 'pi pi-moon' : 'pi pi-sun'"
      severity="secondary"
      @click="toggleDarkMode"
    />
    <div class="relative">
      <Button
        v-styleclass="{ selector: '@next', enterFromClass: 'hidden', enterActiveClass: 'animate-scalein', leaveToClass: 'hidden', leaveActiveClass: 'animate-fadeout', hideOnOutsideClick: true }"
        icon="pi pi-palette"
        type="button"
        rounded
      />
      <AppConfigurator />
    </div>
  </div>
</template>
