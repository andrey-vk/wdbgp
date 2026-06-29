<script setup lang="ts">
// @ts-nocheck
import { CustomerService } from '@/service/CustomerService';
import { ProductService } from '@/service/ProductService';
import { FilterMatchMode, FilterOperator } from '@primevue/core/api';
import { onBeforeMount, reactive, ref } from 'vue';

const customers1 = ref(null);
const customers2 = ref(null);
const customers3 = ref(null);
const filters1 = ref(null);
const loading1 = ref(null);
const balanceFrozen = ref(false);
const products = ref(null);
const expandedRows = ref([]);
const statuses = reactive(['unqualified', 'qualified', 'new', 'negotiation', 'renewal', 'proposal']);
const representatives = reactive([
    { name: 'Amy Elsner', image: 'amyelsner.png' },
    { name: 'Anna Fali', image: 'annafali.png' },
    { name: 'Asiya Javayant', image: 'asiyajavayant.png' },
    { name: 'Bernardo Dominic', image: 'bernardodominic.png' },
    { name: 'Elwin Sharvill', image: 'elwinsharvill.png' },
    { name: 'Ioni Bowcher', image: 'ionibowcher.png' },
    { name: 'Ivan Magalhaes', image: 'ivanmagalhaes.png' },
    { name: 'Onyama Limba', image: 'onyamalimba.png' },
    { name: 'Stephen Shaw', image: 'stephenshaw.png' },
    { name: 'XuXue Feng', image: 'xuxuefeng.png' }
]);

function getOrderSeverity(order) {
    switch (order.status) {
        case 'DELIVERED':
            return 'success';

        case 'CANCELLED':
            return 'danger';

        case 'PENDING':
            return 'warn';

        case 'RETURNED':
            return 'info';

        default:
            return null;
    }
}

function getSeverity(status) {
    switch (status) {
        case 'unqualified':
            return 'danger';

        case 'qualified':
            return 'success';

        case 'new':
            return 'info';

        case 'negotiation':
            return 'warn';

        case 'renewal':
            return null;
    }
}

function getStockSeverity(product) {
    switch (product.inventoryStatus) {
        case 'INSTOCK':
            return 'success';

        case 'LOWSTOCK':
            return 'warn';

        case 'OUTOFSTOCK':
            return 'danger';

        default:
            return null;
    }
}

onBeforeMount(() => {
    ProductService.getProductsWithOrdersSmall().then((data) => (products.value = data));
    CustomerService.getCustomersLarge().then((data) => {
        customers1.value = data;
        loading1.value = false;
        customers1.value.forEach((customer) => (customer.date = new Date(customer.date)));
    });
    CustomerService.getCustomersLarge().then((data) => (customers2.value = data));
    CustomerService.getCustomersMedium().then((data) => (customers3.value = data));

    initFilters1();
});

function initFilters1() {
    filters1.value = {
        global: { value: null, matchMode: FilterMatchMode.CONTAINS },
        name: { operator: FilterOperator.AND, constraints: [{ value: null, matchMode: FilterMatchMode.STARTS_WITH }] },
        'country.name': { operator: FilterOperator.AND, constraints: [{ value: null, matchMode: FilterMatchMode.STARTS_WITH }] },
        representative: { value: null, matchMode: FilterMatchMode.IN },
        date: { operator: FilterOperator.AND, constraints: [{ value: null, matchMode: FilterMatchMode.DATE_IS }] },
        balance: { operator: FilterOperator.AND, constraints: [{ value: null, matchMode: FilterMatchMode.EQUALS }] },
        status: { operator: FilterOperator.OR, constraints: [{ value: null, matchMode: FilterMatchMode.EQUALS }] },
        activity: { value: [0, 100], matchMode: FilterMatchMode.BETWEEN },
        verified: { value: null, matchMode: FilterMatchMode.EQUALS }
    };
}

function expandAll() {
    expandedRows.value = products.value.reduce((acc, p) => (acc[p.id] = true) && acc, {});
}

function collapseAll() {
    expandedRows.value = null;
}

function formatCurrency(value) {
    return value.toLocaleString('en-US', { style: 'currency', currency: 'USD' });
}

function formatDate(value) {
    return value.toLocaleDateString('en-US', {
        day: '2-digit',
        month: '2-digit',
        year: 'numeric'
    });
}

function calculateCustomerTotal(name) {
    let total = 0;
    if (customers3.value) {
        for (const customer of customers3.value) {
            if (customer.representative.name === name) {
                total++;
            }
        }
    }

    return total;
}
</script>

<template>
  <div class="card">
    <div class="font-semibold text-xl mb-4">
      Filtering
    </div>
    <DataTable
      v-model:filters="filters1"
      :value="customers1"
      :paginator="true"
      :rows="10"
      data-key="id"
      :row-hover="true"
      filter-display="menu"
      :loading="loading1"
      :filters="filters1"
      :global-filter-fields="['name', 'country.name', 'representative.name', 'balance', 'status']"
      show-gridlines
    >
      <template #header>
        <div class="flex justify-between">
          <Button
            type="button"
            icon="pi pi-filter-slash"
            label="Clear"
            outlined
            @click="clearFilter()"
          />
          <IconField>
            <InputIcon>
              <i class="pi pi-search" />
            </InputIcon>
            <InputText
              v-model="filters1['global'].value"
              placeholder="Keyword Search"
            />
          </IconField>
        </div>
      </template>
      <template #empty>
        No customers found.
      </template>
      <template #loading>
        Loading customers data. Please wait.
      </template>
      <Column
        field="name"
        header="Name"
        style="min-width: 12rem"
      >
        <template #body="{ data }">
          {{ data.name }}
        </template>
        <template #filter="{ filterModel }">
          <InputText
            v-model="filterModel.value"
            type="text"
            placeholder="Search by name"
          />
        </template>
      </Column>
      <Column
        header="Country"
        filter-field="country.name"
        style="min-width: 12rem"
      >
        <template #body="{ data }">
          <div class="flex items-center gap-2">
            <img
              alt="flag"
              src="https://primefaces.org/cdn/primevue/images/flag/flag_placeholder.png"
              :class="`flag flag-${data.country.code}`"
              style="width: 24px"
            >
            <span>{{ data.country.name }}</span>
          </div>
        </template>
        <template #filter="{ filterModel }">
          <InputText
            v-model="filterModel.value"
            type="text"
            placeholder="Search by country"
          />
        </template>
        <template #filterclear="{ filterCallback }">
          <Button
            type="button"
            icon="pi pi-times"
            severity="secondary"
            @click="filterCallback()"
          />
        </template>
        <template #filterapply="{ filterCallback }">
          <Button
            type="button"
            icon="pi pi-check"
            severity="success"
            @click="filterCallback()"
          />
        </template>
      </Column>
      <Column
        header="Agent"
        filter-field="representative"
        :show-filter-match-modes="false"
        :filter-menu-style="{ width: '14rem' }"
        style="min-width: 14rem"
      >
        <template #body="{ data }">
          <div class="flex items-center gap-2">
            <img
              :alt="data.representative.name"
              :src="`https://primefaces.org/cdn/primevue/images/avatar/${data.representative.image}`"
              style="width: 32px"
            >
            <span>{{ data.representative.name }}</span>
          </div>
        </template>
        <template #filter="{ filterModel }">
          <MultiSelect
            v-model="filterModel.value"
            :options="representatives"
            option-label="name"
            placeholder="Any"
          >
            <template #option="slotProps">
              <div class="flex items-center gap-2">
                <img
                  :alt="slotProps.option.name"
                  :src="`https://primefaces.org/cdn/primevue/images/avatar/${slotProps.option.image}`"
                  style="width: 32px"
                >
                <span>{{ slotProps.option.name }}</span>
              </div>
            </template>
          </MultiSelect>
        </template>
      </Column>
      <Column
        header="Date"
        filter-field="date"
        data-type="date"
        style="min-width: 10rem"
      >
        <template #body="{ data }">
          {{ formatDate(data.date) }}
        </template>
        <template #filter="{ filterModel }">
          <DatePicker
            v-model="filterModel.value"
            date-format="mm/dd/yy"
            placeholder="mm/dd/yyyy"
          />
        </template>
      </Column>
      <Column
        header="Balance"
        filter-field="balance"
        data-type="numeric"
        style="min-width: 10rem"
      >
        <template #body="{ data }">
          {{ formatCurrency(data.balance) }}
        </template>
        <template #filter="{ filterModel }">
          <InputNumber
            v-model="filterModel.value"
            mode="currency"
            currency="USD"
            locale="en-US"
          />
        </template>
      </Column>
      <Column
        header="Status"
        field="status"
        :filter-menu-style="{ width: '14rem' }"
        style="min-width: 12rem"
      >
        <template #body="{ data }">
          <Tag
            :value="data.status"
            :severity="getSeverity(data.status)"
          />
        </template>
        <template #filter="{ filterModel }">
          <Select
            v-model="filterModel.value"
            :options="statuses"
            placeholder="Select One"
            show-clear
          >
            <template #option="slotProps">
              <Tag
                :value="slotProps.option"
                :severity="getSeverity(slotProps.option)"
              />
            </template>
          </Select>
        </template>
      </Column>
      <Column
        field="activity"
        header="Activity"
        :show-filter-match-modes="false"
        style="min-width: 12rem"
      >
        <template #body="{ data }">
          <ProgressBar
            :value="data.activity"
            :show-value="false"
            style="height: 6px"
          />
        </template>
        <template #filter="{ filterModel }">
          <Slider
            v-model="filterModel.value"
            range
            class="m-4"
          />
          <div class="flex items-center justify-between px-2">
            <span>{{ filterModel.value ? filterModel.value[0] : 0 }}</span>
            <span>{{ filterModel.value ? filterModel.value[1] : 100 }}</span>
          </div>
        </template>
      </Column>
      <Column
        field="verified"
        header="Verified"
        data-type="boolean"
        body-class="text-center"
        style="min-width: 8rem"
      >
        <template #body="{ data }">
          <i
            class="pi"
            :class="{ 'pi-check-circle text-green-500 ': data.verified, 'pi-times-circle text-red-500': !data.verified }"
          />
        </template>
        <template #filter="{ filterModel }">
          <label
            for="verified-filter"
            class="font-bold"
          > Verified </label>
          <Checkbox
            v-model="filterModel.value"
            :indeterminate="filterModel.value === null"
            binary
            input-id="verified-filter"
          />
        </template>
      </Column>
    </DataTable>
  </div>

  <div class="card">
    <div class="font-semibold text-xl mb-4">
      Frozen Columns
    </div>
    <ToggleButton
      v-model="balanceFrozen"
      on-icon="pi pi-lock"
      off-icon="pi pi-lock-open"
      on-label="Balance"
      off-label="Balance"
    />

    <DataTable
      :value="customers2"
      scrollable
      scroll-height="400px"
      class="mt-6"
    >
      <Column
        field="name"
        header="Name"
        style="min-width: 200px"
        frozen
        class="font-bold"
      />
      <Column
        field="id"
        header="Id"
        style="min-width: 100px"
      />
      <Column
        field="name"
        header="Name"
        style="min-width: 200px"
      />
      <Column
        field="country.name"
        header="Country"
        style="min-width: 200px"
      />
      <Column
        field="date"
        header="Date"
        style="min-width: 200px"
      />
      <Column
        field="company"
        header="Company"
        style="min-width: 200px"
      />
      <Column
        field="status"
        header="Status"
        style="min-width: 200px"
      />
      <Column
        field="activity"
        header="Activity"
        style="min-width: 200px"
      />
      <Column
        field="representative.name"
        header="Representative"
        style="min-width: 200px"
      />
      <Column
        field="balance"
        header="Balance"
        style="min-width: 200px"
        align-frozen="right"
        :frozen="balanceFrozen"
      >
        <template #body="{ data }">
          <span class="font-bold">{{ formatCurrency(data.balance) }}</span>
        </template>
      </Column>
    </DataTable>
  </div>

  <div class="card">
    <div class="font-semibold text-xl mb-4">
      Row Expansion
    </div>
    <DataTable
      v-model:expanded-rows="expandedRows"
      :value="products"
      data-key="id"
      table-style="min-width: 60rem"
    >
      <template #header>
        <div class="flex flex-wrap justify-end gap-2">
          <Button
            text
            icon="pi pi-plus"
            label="Expand All"
            @click="expandAll"
          />
          <Button
            text
            icon="pi pi-minus"
            label="Collapse All"
            @click="collapseAll"
          />
        </div>
      </template>
      <Column
        expander
        style="width: 5rem"
      />
      <Column
        field="name"
        header="Name"
      />
      <Column header="Image">
        <template #body="slotProps">
          <img
            :src="`https://primefaces.org/cdn/primevue/images/product/${slotProps.data.image}`"
            :alt="slotProps.data.image"
            class="shadow-lg"
            width="64"
          >
        </template>
      </Column>
      <Column
        field="price"
        header="Price"
      >
        <template #body="slotProps">
          {{ formatCurrency(slotProps.data.price) }}
        </template>
      </Column>
      <Column
        field="category"
        header="Category"
      />
      <Column
        field="rating"
        header="Reviews"
      >
        <template #body="slotProps">
          <Rating
            :model-value="slotProps.data.rating"
            readonly
          />
        </template>
      </Column>
      <Column header="Status">
        <template #body="slotProps">
          <Tag
            :value="slotProps.data.inventoryStatus"
            :severity="getStockSeverity(slotProps.data)"
          />
        </template>
      </Column>
      <template #expansion="slotProps">
        <div class="p-4">
          <h5>Orders for {{ slotProps.data.name }}</h5>
          <DataTable :value="slotProps.data.orders">
            <Column
              field="id"
              header="Id"
              sortable
            />
            <Column
              field="customer"
              header="Customer"
              sortable
            />
            <Column
              field="date"
              header="Date"
              sortable
            />
            <Column
              field="amount"
              header="Amount"
              sortable
            >
              <template #body="bodySlotProps">
                {{ formatCurrency(bodySlotProps.data.amount) }}
              </template>
            </Column>
            <Column
              field="status"
              header="Status"
              sortable
            >
              <template #body="bodySlotProps">
                <Tag
                  :value="bodySlotProps.data.status.toLowerCase()"
                  :severity="getOrderSeverity(bodySlotProps.data)"
                />
              </template>
            </Column>
            <Column header-style="width:4rem">
              <template #body>
                <Button icon="pi pi-search" />
              </template>
            </Column>
          </DataTable>
        </div>
      </template>
    </DataTable>
  </div>

  <div class="card">
    <div class="font-semibold text-xl mb-4">
      Grouping
    </div>
    <DataTable
      :value="customers3"
      row-group-mode="subheader"
      group-rows-by="representative.name"
      sort-mode="single"
      sort-field="representative.name"
      :sort-order="1"
      scrollable
      scroll-height="400px"
      table-style="min-width: 50rem"
    >
      <template #groupheader="slotProps">
        <div class="flex items-center gap-2">
          <img
            :alt="slotProps.data.representative.name"
            :src="`https://primefaces.org/cdn/primevue/images/avatar/${slotProps.data.representative.image}`"
            width="32"
            style="vertical-align: middle"
          >
          <span>{{ slotProps.data.representative.name }}</span>
        </div>
      </template>
      <Column
        field="representative.name"
        header="Representative"
      />
      <Column
        field="name"
        header="Name"
        style="min-width: 200px"
      />
      <Column
        field="country"
        header="Country"
        style="min-width: 200px"
      >
        <template #body="slotProps">
          <div class="flex items-center gap-2">
            <img
              alt="flag"
              src="https://primefaces.org/cdn/primevue/images/flag/flag_placeholder.png"
              :class="`flag flag-${slotProps.data.country.code}`"
              style="width: 24px"
            >
            <span>{{ slotProps.data.country.name }}</span>
          </div>
        </template>
      </Column>
      <Column
        field="company"
        header="Company"
        style="min-width: 200px"
      />
      <Column
        field="status"
        header="Status"
        style="min-width: 200px"
      >
        <template #body="slotProps">
          <Tag
            :value="slotProps.data.status"
            :severity="getSeverity(slotProps.data.status)"
          />
        </template>
      </Column>
      <Column
        field="date"
        header="Date"
        style="min-width: 200px"
      />
      <template #groupfooter="slotProps">
        <div class="flex justify-end font-bold w-full">
          Total Customers: {{ calculateCustomerTotal(slotProps.data.representative.name) }}
        </div>
      </template>
    </DataTable>
  </div>
</template>

<style scoped lang="scss">
:deep(.p-datatable-frozen-tbody) {
    font-weight: bold;
}

:deep(.p-datatable-scrollable .p-frozen-column) {
    font-weight: bold;
}
</style>
