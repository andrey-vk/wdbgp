<script setup lang="ts">
import { useLayout } from './composables/layout';
import { onBeforeUnmount, ref, watch } from 'vue';
import { useRoute } from 'vue-router';
import AppMenu from './AppMenu.vue';

const { layoutState, isDesktop, hasOpenOverlay } = useLayout();
const route = useRoute();
const sidebarRef = ref<HTMLElement | null>(null);
let outsideClickListener: ((e: MouseEvent) => void) | null = null;

watch(
    () => route.path,
    (newPath) => {
        if (isDesktop()) layoutState.activePath = null;
        else layoutState.activePath = newPath;

        layoutState.overlayMenuActive = false;
        layoutState.mobileMenuActive = false;
        layoutState.menuHoverActive = false;
    },
    { immediate: true }
);

watch(hasOpenOverlay, (newVal) => {
    if (isDesktop()) {
        if (newVal) bindOutsideClickListener();
        else unbindOutsideClickListener();
    }
});

const bindOutsideClickListener = () => {
    if (!outsideClickListener) {
        outsideClickListener = (event: MouseEvent) => {
            if (isOutsideClicked(event)) {
                layoutState.overlayMenuActive = false;
            }
        };

        document.addEventListener('click', outsideClickListener);
    }
};

const unbindOutsideClickListener = () => {
    if (outsideClickListener) {
        document.removeEventListener('click', outsideClickListener);
        outsideClickListener = null;
    }
};

const isOutsideClicked = (event: MouseEvent) => {
    const topbarButtonEl = document.querySelector('.layout-menu-button');

    if (!sidebarRef.value) return false;
    return !(sidebarRef.value.isSameNode(event.target as Node) || sidebarRef.value.contains(event.target as Node) || topbarButtonEl?.isSameNode(event.target as Node) || topbarButtonEl?.contains(event.target as Node));
};

onBeforeUnmount(() => {
    unbindOutsideClickListener();
});
</script>

<template>
  <div
    ref="sidebarRef"
    class="layout-sidebar"
  >
    <AppMenu />
  </div>
</template>
