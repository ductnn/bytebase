<template>
  <div class="w-full relative">
    <NTransfer
      v-model:value="selectedValueList"
      style="height: 512px"
      :disabled="disabled"
      :options="sourceTransferOptions"
      :render-source-list="renderSourceList"
      :render-target-list="renderTargetList"
      :source-filterable="true"
      :source-filter-placeholder="$t('common.filter-by-name')"
    />
    <div
      v-if="loading"
      class="z-1 absolute inset-0 bg-white bg-opacity-80 flex flex-row justify-center items-center"
    >
      <BBSpin size="large" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { orderBy } from "lodash-es";
import type { TransferRenderSourceList, TreeOption } from "naive-ui";
import { NTransfer, NTree } from "naive-ui";
import { computed, h, onMounted, ref, watch } from "vue";
import { BBSpin } from "@/bbkit";
import {
  useDatabaseV1Store,
  useDBSchemaV1Store,
  useProjectByName,
} from "@/store";
import {
  databaseNamePrefix,
  instanceNamePrefix,
} from "@/store/modules/v1/common";
import { useDatabaseV1List } from "@/store/modules/v1/databaseList";
import type { DatabaseResource } from "@/types";
import { DatabaseMetadataView } from "@/types/proto/v1/database_service";
import { wrapRefAsPromise } from "@/utils";
import Label from "./Label.vue";
import type { DatabaseTreeOption, DatabaseResourceType } from "./common";
import {
  flattenTreeOptions,
  getSchemaOrTableTreeOptions,
  mapTreeOptions,
} from "./common";

const props = defineProps<{
  disabled?: boolean;
  projectName: string;
  includeCloumn: boolean;
  databaseResources: DatabaseResource[];
}>();

const emit = defineEmits<{
  (
    e: "update:databaseResources",
    databaseResourceList: DatabaseResource[]
  ): void;
}>();

const databaseStore = useDatabaseV1Store();
const dbSchemaStore = useDBSchemaV1Store();
const { project } = useProjectByName(props.projectName);

const parseKeyToResource = (key: string): DatabaseResource | undefined => {
  const sections = key.split("/");
  const resource: DatabaseResource = {
    databaseName: "",
  };

  while (sections.length > 0) {
    const keyword = sections.shift() as DatabaseResourceType | "instances";
    const data = sections.shift() || "";

    switch (keyword) {
      case "instances":
        resource.instanceResourceId = data;
        break;
      case "databases":
        if (!resource.instanceResourceId) {
          return;
        }
        resource.databaseName = `${instanceNamePrefix}${resource.instanceResourceId}/${databaseNamePrefix}${data}`;
        break;
      case "schemas":
        resource.schema = data;
        break;
      case "tables":
        resource.table = data;
        break;
      case "columns":
        resource.column = data;
        break;
      default:
        return;
    }
  }

  if (!resource.databaseName) {
    return;
  }

  return resource;
};

const parseResourceToKey = (resource: DatabaseResource): string => {
  const data = [
    resource.databaseName,
    "schemas",
    resource.schema,
    "tables",
    resource.table,
    "columns",
    resource.column,
  ];

  while (data.length > 0) {
    const item = data.pop();
    if (!item) {
      data.pop();
      continue;
    }
    data.push(item);
    break;
  }

  return data.join("/");
};

const selectedValueList = ref<string[]>([]);
const expandedKeys = ref<string[]>([]);
const loading = ref(true);

const cascadeLoopTreeNode = (
  treeNode: DatabaseTreeOption,
  callback: (node: DatabaseTreeOption) => void
) => {
  callback(treeNode);
  for (const child of treeNode?.children ?? []) {
    cascadeLoopTreeNode(child, callback);
  }
};

onMounted(async () => {
  await wrapRefAsPromise(useDatabaseV1List(props.projectName).ready, true);

  const selectedKeys = props.databaseResources.map(parseResourceToKey);
  const databaseNames = new Set(
    selectedKeys.map((key) => key.split("/schemas/")[0]).filter((key) => key)
  );
  await Promise.all(
    [...databaseNames].map(async (databaseName) => {
      await dbSchemaStore.getOrFetchDatabaseMetadata({
        database: databaseName,
        view: DatabaseMetadataView.DATABASE_METADATA_VIEW_BASIC,
      });
    })
  );

  const newCheckedKeys = new Set(selectedKeys);
  const newExpandedKeys = new Set(
    // expand parents for selected keys
    selectedKeys
      .map((key) => {
        const pathes = parseKeyToPathes(key);
        // key: {db}/schemas/{schema}
        // expaned: [{db}]
        //
        // key: {db}/schemas/{schema}/tables/{table}
        // expaned: [{db}, {db}/schemas/{schema}]
        //
        // key: {db}/schemas/{schema}/tables/{table}/columns/{column}
        // expaned: [{db}, {db}/schemas/{schema}, {db}/schemas/{schema}/tables/{table}]
        pathes.pop();
        return pathes;
      })
      .flat()
  );

  for (const selectedKey of selectedKeys) {
    const checkedNode = sourceTransferOptions.value.find(
      (option) => option.value === selectedKey
    );
    if (checkedNode) {
      // check and expand all children
      cascadeLoopTreeNode(checkedNode, (treeNode) => {
        newCheckedKeys.add(treeNode.value);
        if (treeNode.children) {
          newExpandedKeys.add(treeNode.value);
        }
      });
    }
  }

  selectedValueList.value = [...newCheckedKeys];
  expandedKeys.value = [...newExpandedKeys];

  loading.value = false;
});

const parseKeyToPathes = (key: string): string[] => {
  if (!key) {
    return [];
  }

  const sections = key.split("/");
  const nodePrefx = new Set(["schemas", "tables", "columns"]);
  const resp: string[] = [];
  const tmp: string[] = [];

  for (const section of sections) {
    if (nodePrefx.has(section)) {
      resp.push(tmp.join("/"));
    }
    tmp.push(section);
  }

  if (tmp.length > 0) {
    resp.push(tmp.join("/"));
  }

  return resp;
};

const databaseList = computed(() => {
  const list = orderBy(
    databaseStore.databaseListByProject(project.value.name),
    [
      (db) => db.effectiveEnvironmentEntity.order,
      (db) => db.effectiveEnvironmentEntity.title,
      (db) => db.databaseName,
      (db) => db.instanceResource.title,
    ],
    ["desc", "asc", "asc", "asc"]
  );
  return list;
});

const sourceTreeOptions = computed(() => {
  return mapTreeOptions({
    databaseList: databaseList.value,
    includeCloumn: props.includeCloumn,
  });
});

const sourceTransferOptions = computed((): DatabaseTreeOption[] => {
  const options = flattenTreeOptions(sourceTreeOptions.value);
  return options;
});

const onTreeNodeLoad = async (node: TreeOption) => {
  const treeNode = node as DatabaseTreeOption;
  if (treeNode.level === "databases") {
    await dbSchemaStore.getOrFetchDatabaseMetadata({
      database: treeNode.value,
      view: DatabaseMetadataView.DATABASE_METADATA_VIEW_BASIC,
    });
    const database = databaseStore.getDatabaseByName(treeNode.value);
    const children = getSchemaOrTableTreeOptions({
      database,
      includeCloumn: props.includeCloumn,
    });
    if (children && children.length > 0) {
      treeNode.children = children;
      treeNode.isLeaf = false;
    } else {
      treeNode.isLeaf = true;
    }
  }
};

const renderSourceList: TransferRenderSourceList = ({ onCheck, pattern }) => {
  return h(NTree, {
    keyField: "value",
    cascade: true,
    allowCheckingNotLoaded: true,
    checkable: true,
    selectable: false,
    checkOnClick: true,
    disabled: props.disabled,
    data: sourceTreeOptions.value,
    blockLine: true,
    virtualScroll: true,
    style: "height: 428px", // since <NTransfer> height is 512
    renderLabel: ({ option }: { option: TreeOption }) => {
      return h(Label, {
        option: option as DatabaseTreeOption,
        keyword: pattern,
      });
    },
    pattern,
    showIrrelevantNodes: false,
    expandedKeys: expandedKeys.value,
    checkedKeys: selectedValueList.value,
    onLoad: onTreeNodeLoad,
    onUpdateExpandedKeys: (keys: string[]) => {
      expandedKeys.value = keys;
    },
    onUpdateCheckedKeys: async (
      checkedKeys: string[],
      _: Array<TreeOption | null>,
      meta: { node: TreeOption | null; action: "check" | "uncheck" }
    ) => {
      if (!meta.node) {
        return;
      }

      const newCheckedKeys = new Set(checkedKeys);

      const treeNode = meta.node as DatabaseTreeOption;
      if (meta.action === "check") {
        await onTreeNodeLoad(treeNode);
        // refresh node in case the schema is updated
        const checkedNode = sourceTransferOptions.value.find(
          (option) => option.value === treeNode.value
        );
        if (checkedNode) {
          // check and expand all children
          cascadeLoopTreeNode(checkedNode, (treeNode) => {
            newCheckedKeys.add(treeNode.value);
            if (treeNode.children) {
              expandedKeys.value.push(treeNode.value);
            }
          });
        }
      }

      onCheck([...newCheckedKeys]);
    },
  });
};

const targetTreeOptions = computed(() => {
  if (selectedValueList.value.length === 0) {
    return [];
  }

  const nodes = mapTreeOptions({
    databaseList: databaseList.value,
    filterValueList: selectedValueList.value,
    includeCloumn: props.includeCloumn,
  });

  for (const databaseNode of nodes) {
    if (!databaseNode.children || databaseNode.children.length === 0) {
      databaseNode.isLeaf = true;
    }
  }
  return nodes;
});

const renderTargetList: TransferRenderSourceList = () => {
  return h(NTree, {
    keyField: "value",
    checkable: false,
    selectable: false,
    defaultExpandAll: true,
    disabled: props.disabled,
    data: targetTreeOptions.value,
    blockLine: true,
    virtualScroll: true,
    style: "height: 468px", // since <NTransfer> height is 512
    renderLabel: ({ option }: { option: TreeOption }) => {
      return h(Label, {
        option: option as DatabaseTreeOption,
      });
    },
    showIrrelevantNodes: false,
    checkedKeys: selectedValueList.value,
  });
};

watch(selectedValueList, (selectedValueList) => {
  const orderedList = orderBy(selectedValueList, (item) => item.length, "asc");
  const filteredKeyList: string[] = [];
  for (const key of orderedList) {
    const parentExisted = filteredKeyList.some((parent) =>
      key.startsWith(`${parent}/`)
    );
    // If the parent node is selected, means all children should be selected.
    // So we can ignore the children.
    // For example, select table "employee"."public"."employee" and all its fields "emp_no" & "name",
    // we only need the "employee"."public"."employee" to build the database resource,
    // and the expression only need table level too (ignore the column means column = "*")
    if (!parentExisted) {
      filteredKeyList.push(key);
    }
  }

  emit(
    "update:databaseResources",
    filteredKeyList
      .map(parseKeyToResource)
      .filter((data) => data) as DatabaseResource[]
  );
});
</script>
