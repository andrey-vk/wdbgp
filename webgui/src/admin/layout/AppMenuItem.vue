<script setup lang="ts">
// @ts-nocheck
import { useLayout } from './composables/layout';
import { computed } from 'vue';

const { layoutState, isDesktop } = useLayout();

const props = defineProps({
    item: {
        type: Object,
        default: () => ({})
    },
    root: {
        type: Boolean,
        default: true
    },
    parentPath: {
        type: String,
        default: null
    }
});

const fullPath = computed(() => (props.item.path ? (props.parentPath ? props.parentPath + props.item.path : props.item.path) : null));

const isActive = computed(() => {
    return props.item.path ? layoutState.activePath?.startsWith(fullPath.value) : layoutState.activePath === props.item.to;
});

const itemClick = (event, item) => {
    if (item.disabled) {
        event.preventDefault();
        return;
    }

    if (item.command) {
        item.command({ originalEvent: event, item: item });
    }

    if (item.items) {
        if (isActive.value) {
            layoutState.activePath = layoutState.activePath.replace(item.path, '');
        } else {
            layoutState.activePath = fullPath.value;
            layoutState.menuHoverActive = true;
        }
    } else {
        layoutState.overlayMenuActive = false;
        layoutState.mobileMenuActive = false;
        layoutState.menuHoverActive = false;
    }
};

const onMouseEnter = () => {
    if (isDesktop() && props.root && props.item.items && layoutState.menuHoverActive) {
        layoutState.activePath = fullPath.value;
    }
};
</script>

<template>
  <li :class="{ 'layout-root-menuitem': root, 'active-menuitem': isActive }">
    <div
      v-if="root && item.visible !== false"
      class="layout-menuitem-root-text"
    >
      {{ item.label }}
    </div>
    <a
      v-if="(!item.to || item.items) && item.visible !== false"
      :href="item.url"
      :class="item.class"
      :target="item.target"
      tabindex="0"
      @click="itemClick($event, item)"
      @mouseenter="onMouseEnter"
    >
      <i
        :class="item.icon"
        class="layout-menuitem-icon"
      />
      <span class="layout-menuitem-text">{{ item.label }}</span>
      <i
        v-if="item.items"
        class="pi pi-fw pi-angle-down layout-submenu-toggler"
      />
    </a>
    <router-link
      v-if="item.to && !item.items && item.visible !== false"
      exact-active-class="active-route"
      :class="item.class"
      tabindex="0"
      :to="item.to"
      @click="itemClick($event, item)"
      @mouseenter="onMouseEnter"
    >
      <i
        :class="item.icon"
        class="layout-menuitem-icon"
      />
      <span class="layout-menuitem-text">{{ item.label }}</span>
      <i
        v-if="item.items"
        class="pi pi-fw pi-angle-down layout-submenu-toggler"
      />
    </router-link>
    <Transition
      v-if="item.items && item.visible !== false"
      name="layout-submenu"
    >
      <ul
        v-show="root ? true : isActive"
        class="layout-submenu"
      >
        <app-menu-item
          v-for="child in item.items"
          :key="child.label + '_' + (child.to || child.path)"
          :item="child"
          :root="false"
          :parent-path="fullPath"
        />
      </ul>
    </Transition>
  </li>
</template>
