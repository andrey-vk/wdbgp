<script setup lang="ts">
// @ts-nocheck
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import AppMenuItem from './AppMenuItem.vue';

const { t } = useI18n();

const model = computed(() => [
    {
        label: t('menu.group.main'),
        items: [
            { label: t('menu.dashboard'), icon: 'pi pi-fw pi-home', to: { name: 'dashboard' } },
        ]
    },
    {
        label: t('menu.group.configuration'),
        items: [
            { label: t('menu.users'),       icon: 'pi pi-fw pi-users',    to: { name: 'users' } },
            { label: t('menu.modes'),       icon: 'pi pi-fw pi-tags',     to: { name: 'modes' } },
            { label: t('menu.feeds'),       icon: 'pi pi-fw pi-download', to: { name: 'feeds' } },
            { label: t('menu.adapters'),    icon: 'pi pi-fw pi-code',     to: { name: 'adapters' } },
        ]
    },
    {
        label: t('menu.group.system'),
        items: [
            { label: t('menu.settings'),    icon: 'pi pi-fw pi-cog',      to: { name: 'settings' } },
            { label: t('menu.debug'),       icon: 'pi pi-fw pi-search',   to: { name: 'debug' } },
        ]
    },
]);
</script>

<template>
  <ul class="layout-menu">
    <template
      v-for="(item, i) in model"
      :key="item"
    >
      <app-menu-item
        v-if="!item.separator"
        :item="item"
        :index="i"
      />
      <li
        v-if="item.separator"
        class="menu-separator"
      />
    </template>
  </ul>
</template>

<style lang="scss" scoped></style>
