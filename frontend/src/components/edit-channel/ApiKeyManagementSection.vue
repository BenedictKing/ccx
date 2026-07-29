<template>
  <div class="api-key-management-section">
    <v-card variant="outlined" rounded="lg" :color="hasConfigurableKeys ? undefined : 'error'">
      <v-card-title class="d-flex align-center justify-space-between pa-4 pb-2">
        <div class="d-flex align-center ga-2">
          <v-icon :color="hasConfigurableKeys ? 'primary' : 'error'">mdi-key</v-icon>
          <span class="section-title">
            {{ t('channelCard.apiKeyManagement') }}<span v-if="serviceType !== 'copilot'"> *</span>
          </span>
          <v-chip v-if="!hasConfigurableKeys" size="x-small" color="error" variant="tonal">
            {{ t('channelEditor.auth.apiKeyRequired') }}
          </v-chip>
        </div>
        <v-chip size="small" color="info" variant="tonal">
          {{ t('addChannel.apiKeyLoadBalance') }}
        </v-chip>
      </v-card-title>

      <v-card-text class="pt-2">
        <v-alert
          v-if="restoringKey || restoringKeyModel"
          color="info"
          variant="tonal"
          density="compact"
          class="mb-3"
          role="status"
          aria-live="polite"
        >
          <div class="d-flex align-center ga-2">
            <v-progress-circular indeterminate size="16" width="2" />
            <span>{{ t('channelCard.restoreKeyInProgress') }}</span>
          </div>
        </v-alert>

        <div v-if="providerId === 'volcengine' && accountUid" class="d-flex align-center justify-space-between ga-3 flex-wrap mb-3">
          <div class="d-flex align-center ga-2">
            <v-icon color="primary" size="small">mdi-shield-key-outline</v-icon>
            <span class="text-body-2 font-weight-medium">{{ t('volcengineAccessKey.title') }}</span>
          </div>
          <v-btn
            size="small"
            variant="text"
            color="primary"
            href="https://console.volcengine.com/iam/keymanage/"
            target="_blank"
            rel="noopener noreferrer"
          >
            <v-icon start size="small">mdi-open-in-new</v-icon>
            {{ t('volcengineAccessKey.openConsole') }}
          </v-btn>
        </div>
        <v-progress-linear
          v-if="providerId === 'volcengine' && volcengineCredentialsLoading"
          indeterminate
          color="primary"
          class="mb-3"
        />
        <v-alert
          v-if="providerId === 'volcengine' && volcengineCredentialsError"
          color="error"
          variant="tonal"
          density="compact"
          class="mb-3"
        >
          {{ volcengineCredentialsError }}
        </v-alert>

        <div v-if="providerId === 'kimi' && accountUid" class="d-flex align-center justify-space-between ga-3 flex-wrap mb-2">
          <div class="d-flex align-center ga-2">
            <v-icon color="primary" size="small">mdi-chart-donut</v-icon>
            <span class="text-body-2 font-weight-medium">{{ t('kimiConsoleToken.title') }}</span>
          </div>
          <v-btn
            size="small"
            variant="text"
            color="primary"
            href="https://www.kimi.com/code/console"
            target="_blank"
            rel="noopener noreferrer"
          >
            <v-icon start size="small">mdi-open-in-new</v-icon>
            {{ t('kimiConsoleToken.openConsole') }}
          </v-btn>
        </div>
        <div v-if="providerId === 'kimi' && accountUid" class="text-caption text-medium-emphasis mb-3">
          {{ t('kimiConsoleToken.hint') }}
        </div>
        <v-progress-linear
          v-if="providerId === 'kimi' && kimiCredentialsLoading"
          indeterminate
          color="primary"
          class="mb-3"
        />
        <v-alert
          v-if="providerId === 'kimi' && kimiCredentialsError"
          color="error"
          variant="tonal"
          density="compact"
          class="mb-3"
        >
          {{ kimiCredentialsError }}
        </v-alert>

        <div v-if="providerId === 'mimo' && accountUid" class="d-flex align-center justify-space-between ga-3 flex-wrap mb-2">
          <div class="d-flex align-center ga-2">
            <v-icon color="primary" size="small">mdi-cookie-cog-outline</v-icon>
            <span class="text-body-2 font-weight-medium">{{ t('mimoConsoleCookie.title') }}</span>
          </div>
          <v-btn
            size="small"
            variant="text"
            color="primary"
            href="https://platform.xiaomimimo.com/console/plan-manage"
            target="_blank"
            rel="noopener noreferrer"
          >
            <v-icon start size="small">mdi-open-in-new</v-icon>
            {{ t('mimoConsoleCookie.openConsole') }}
          </v-btn>
        </div>
        <div v-if="providerId === 'mimo' && accountUid" class="text-caption text-medium-emphasis mb-3">
          {{ t('mimoConsoleCookie.hint') }}
        </div>
        <v-progress-linear
          v-if="providerId === 'mimo' && mimoCredentialsLoading"
          indeterminate
          color="primary"
          class="mb-3"
        />
        <v-alert
          v-if="providerId === 'mimo' && mimoCredentialsError"
          color="error"
          variant="tonal"
          density="compact"
          class="mb-3"
        >
          {{ mimoCredentialsError }}
        </v-alert>

        <div v-if="providerId === 'compshare' && accountUid" class="d-flex align-center justify-space-between ga-3 flex-wrap mb-2">
          <div class="d-flex align-center ga-2">
            <v-icon color="primary" size="small">mdi-gauge</v-icon>
            <span class="text-body-2 font-weight-medium">{{ t('compshareConsoleCookie.title') }}</span>
          </div>
          <v-btn
            size="small"
            variant="text"
            color="primary"
            href="https://console.compshare.cn/light-gpu/model-manage"
            target="_blank"
            rel="noopener noreferrer"
          >
            <v-icon start size="small">mdi-open-in-new</v-icon>
            {{ t('compshareConsoleCookie.openConsole') }}
          </v-btn>
        </div>
        <div v-if="providerId === 'compshare' && accountUid" class="text-caption text-medium-emphasis mb-3">
          {{ t('compshareConsoleCookie.hint') }}
        </div>
        <v-progress-linear
          v-if="providerId === 'compshare' && compshareCredentialsLoading"
          indeterminate
          color="primary"
          class="mb-3"
        />
        <v-alert
          v-if="providerId === 'compshare' && compshareCredentialsError"
          color="error"
          variant="tonal"
          density="compact"
          class="mb-3"
        >
          {{ compshareCredentialsError }}
        </v-alert>

        <v-progress-linear
          v-if="providerId === 'minimax' && minimaxEndpointsLoading"
          indeterminate
          color="primary"
          class="mb-3"
        />
        <v-alert
          v-if="providerId === 'minimax' && minimaxEndpointsError"
          color="error"
          variant="tonal"
          density="compact"
          class="mb-3"
        >
          {{ minimaxEndpointsError }}
        </v-alert>

        <!-- 现有密钥列表（拉黑状态与 provider 用量均归并到对应 Key） -->
        <div v-if="keyRows.length" class="mb-4">
          <v-list density="compact" class="bg-transparent">
            <div v-for="row in keyRows" :key="row.key" class="mb-2">
              <v-list-item
                rounded="lg"
                variant="tonal"
                :color="row.disabled ? 'warning' : duplicateKeyIndex === row.activeIndex ? 'error' : 'surface-variant'"
                :class="{
                  'animate-pulse': duplicateKeyIndex === row.activeIndex,
                  'volcengine-key-row': !!(row.planCredential || row.minimaxEndpoint),
                }"
                @click="(row.planCredential || row.minimaxEndpoint) && toggleCredentialKey(row.key)"
              >
                <template #prepend>
                  <v-icon
                    size="small"
                    :color="row.disabled ? 'warning' : duplicateKeyIndex === row.activeIndex ? 'error' : 'primary'"
                  >
                    {{ row.disabled ? 'mdi-key-alert' : duplicateKeyIndex === row.activeIndex ? 'mdi-alert' : 'mdi-key' }}
                  </v-icon>
                </template>

                <v-list-item-title>
                  <div class="d-flex align-center ga-2 flex-wrap">
                    <code class="text-caption">{{ maskApiKey(row.key) }}</code>
                    <v-chip
                      v-if="row.disabled"
                      size="x-small"
                      :color="disabledKeyColor(row.disabled.reason)"
                      variant="tonal"
                    >
                      {{ t(getDisabledKeyLabel(row.disabled.reason)) }}
                    </v-chip>
                    <v-chip
                      v-else-if="row.enabled === false"
                      size="x-small"
                      color="warning"
                      variant="tonal"
                    >
                      {{ t('channelCard.keyPaused') }}
                    </v-chip>
                    <span v-if="row.disabled?.recoverAt" class="text-caption text-medium-emphasis">
                      {{ t('channelCard.recoverAt') }}: {{ formatDisabledTime(row.disabled.recoverAt) }}
                    </span>
                    <div v-if="!row.disabled" class="d-flex align-center ga-1">
                    <!-- Models 状态标签 -->
                    <v-chip
                      v-if="keyModelsStatus.get(row.key)?.loading"
                      size="x-small"
                      color="info"
                      variant="tonal"
                    >
                      <v-icon start size="12">mdi-loading</v-icon>
                      {{ t('addChannel.checking') }}
                    </v-chip>
                    <v-chip
                      v-else-if="keyModelsStatus.get(row.key)?.success"
                      size="x-small"
                      color="success"
                      variant="tonal"
                    >
                      {{ t('addChannel.modelsCount', { statusCode: keyModelsStatus.get(row.key)?.statusCode ?? 'OK', count: keyModelsStatus.get(row.key)?.modelCount ?? 0 }) }}
                    </v-chip>
                    <v-tooltip
                      v-else-if="keyModelsStatus.get(row.key)?.error"
                      :text="keyModelsStatus.get(row.key)?.error"
                      location="top"
                      max-width="300"
                      content-class="key-tooltip"
                    >
                      <template #activator="{ props: tooltipProps }">
                        <v-chip
                          v-bind="tooltipProps"
                          size="x-small"
                          color="error"
                          variant="tonal"
                        >
                          models {{ keyModelsStatus.get(row.key)?.statusCode || 'ERR' }}
                        </v-chip>
                      </template>
                    </v-tooltip>
                    <!-- 重复密钥标签 -->
                    <v-chip v-if="duplicateKeyIndex === row.activeIndex" size="x-small" color="error" variant="text">
                      {{ t('channelEditor.auth.duplicateKey') }}
                    </v-chip>
                  </div>
                  </div>
                </v-list-item-title>
                <v-list-item-subtitle v-if="row.volcengineCredential" class="mt-1 text-caption">
                  <v-icon size="12" class="mr-1">mdi-chart-timeline-variant</v-icon>
                  <template v-for="(part, idx) in volcengineUsageSummaryParts(row.volcengineCredential)" :key="part.labelKey || idx">
                    <span v-if="idx > 0" class="text-medium-emphasis"> · </span>
                    <span :class="part.colorClass || 'text-medium-emphasis'">{{ part.text }}</span>
                  </template>
                </v-list-item-subtitle>
                <v-list-item-subtitle v-if="row.kimiCredential" class="mt-1 text-caption">
                  <v-icon size="12" class="mr-1">mdi-chart-donut</v-icon>
                  <template v-for="(part, idx) in kimiUsageSummaryParts(row.kimiCredential)" :key="part.labelKey || idx">
                    <span v-if="idx > 0" class="text-medium-emphasis"> · </span>
                    <span :class="part.colorClass || 'text-medium-emphasis'">{{ part.text }}</span>
                  </template>
                </v-list-item-subtitle>
                <v-list-item-subtitle v-if="row.mimoCredential" class="mt-1 text-caption">
                  <v-icon size="12" class="mr-1">mdi-cookie-cog-outline</v-icon>
                  {{ mimoUsageSummary(row.mimoCredential) }}
                </v-list-item-subtitle>
                <v-list-item-subtitle v-if="row.compshareCredential" class="mt-1 text-caption">
                  <v-icon size="12" class="mr-1">mdi-gauge</v-icon>
                  {{ compshareUsageSummary(row.compshareCredential) }}
                </v-list-item-subtitle>
                <v-list-item-subtitle v-if="row.minimaxEndpoint" class="mt-1 text-caption">
                  <v-icon size="12" class="mr-1">mdi-lightning-bolt-outline</v-icon>
                  {{ minimaxUsageSummary(row.minimaxEndpoint) }}
                </v-list-item-subtitle>

                <template #append>
                  <div class="d-flex align-center ga-1" @click.stop>
                    <v-btn
                      v-if="row.disabled"
                      size="small"
                      color="success"
                      variant="tonal"
                      rounded="lg"
                      :loading="restoringKey === row.key"
                      :disabled="!!restoringKey"
                      @click="$emit('restore-key', row.key)"
                    >
                      <v-icon start size="small">mdi-restore</v-icon>
                      {{ t('channelCard.restoreKey') }}
                    </v-btn>
                    <v-tooltip
                      v-else-if="row.enabled === false"
                      :text="t('channelCard.resumeKey')"
                      location="top"
                      :open-delay="150"
                      content-class="key-tooltip"
                    >
                      <template #activator="{ props: tooltipProps }">
                        <v-btn
                          v-bind="tooltipProps"
                          size="small"
                          color="success"
                          variant="tonal"
                          rounded="lg"
                          :loading="suspendingKey === row.key"
                          :disabled="!!suspendingKey"
                          @click="$emit('resume-key', row.key)"
                        >
                          <v-icon start size="small">mdi-play</v-icon>
                          {{ t('channelCard.resumeKey') }}
                        </v-btn>
                      </template>
                    </v-tooltip>
                    <v-tooltip
                      v-else
                      :text="t('channelCard.suspendKey')"
                      location="top"
                      :open-delay="150"
                      content-class="key-tooltip"
                    >
                      <template #activator="{ props: tooltipProps }">
                        <v-btn
                          v-bind="tooltipProps"
                          size="small"
                          color="warning"
                          variant="tonal"
                          rounded="lg"
                          :loading="suspendingKey === row.key"
                          :disabled="!!suspendingKey"
                          @click="$emit('suspend-key', row.key)"
                        >
                          <v-icon start size="small">mdi-pause</v-icon>
                          {{ t('channelCard.suspendKey') }}
                        </v-btn>
                      </template>
                    </v-tooltip>
                    <v-btn
                      v-if="row.planCredential || row.minimaxEndpoint"
                      size="small"
                      color="primary"
                      icon
                      variant="text"
                      :aria-label="planRowTitle()"
                      @click="toggleCredentialKey(row.key)"
                    >
                      <v-icon size="small">
                        {{ expandedCredentialKey === row.key ? 'mdi-chevron-up' : 'mdi-chevron-down' }}
                      </v-icon>
                    </v-btn>
                    <template v-if="!row.disabled && row.activeIndex >= 0">
                  <!-- 置顶/置底：仅首尾密钥显示 -->
                  <v-tooltip
                    v-if="!isAutoManaged && row.activeIndex === apiKeys.length - 1 && apiKeys.length > 1"
                    :text="t('channelCard.moveTop')"
                    location="top"
                    :open-delay="150"
                    content-class="key-tooltip"
                  >
                    <template #activator="{ props: tooltipProps }">
                      <v-btn
                        v-bind="tooltipProps"
                        size="small"
                        color="warning"
                        icon
                        variant="text"
                        rounded="md"
                        @click="moveToTop(row.activeIndex)"
                      >
                        <v-icon size="small">mdi-arrow-up-bold</v-icon>
                      </v-btn>
                    </template>
                  </v-tooltip>
                  <v-tooltip
                    v-if="!isAutoManaged && row.activeIndex === 0 && apiKeys.length > 1"
                    :text="t('channelCard.moveBottom')"
                    location="top"
                    :open-delay="150"
                    content-class="key-tooltip"
                  >
                    <template #activator="{ props: tooltipProps }">
                      <v-btn
                        v-bind="tooltipProps"
                        size="small"
                        color="warning"
                        icon
                        variant="text"
                        rounded="md"
                        @click="moveToBottom(row.activeIndex)"
                      >
                        <v-icon size="small">mdi-arrow-down-bold</v-icon>
                      </v-btn>
                    </template>
                  </v-tooltip>
                    </template>
                  <v-tooltip
                    :text="copiedKey === row.key ? t('channelCard.copied') : t('channelCard.copyKey')"
                    location="top"
                    :open-delay="150"
                    content-class="key-tooltip"
                  >
                    <template #activator="{ props: tooltipProps }">
                      <v-btn
                        v-bind="tooltipProps"
                        size="small"
                        :color="copiedKey === row.key ? 'success' : 'primary'"
                        icon
                        variant="text"
                        @click="copyKey(row.key)"
                      >
                        <v-icon size="small">{{
                          copiedKey === row.key ? 'mdi-check' : 'mdi-content-copy'
                        }}</v-icon>
                      </v-btn>
                    </template>
                  </v-tooltip>
                  <v-tooltip :text="t('addChannel.deleteKey')" location="top" :open-delay="150" content-class="key-tooltip">
                    <template #activator="{ props: tooltipProps }">
                      <v-btn
                        v-bind="tooltipProps"
                        size="small"
                        color="error"
                        icon
                        variant="text"
                        :loading="removingKey === row.key"
                        :disabled="!!removingKey"
                        @click="handleRemoveKey(row)"
                      >
                        <v-icon size="small" color="error">mdi-close</v-icon>
                      </v-btn>
                    </template>
                  </v-tooltip>
                  </div>
                </template>
              </v-list-item>

              <v-expand-transition>
                <div
                  v-if="row.volcengineCredential && expandedCredentialKey === row.key"
                  class="volcengine-key-detail px-4 pt-3 pb-4"
                >
                  <div class="d-flex align-center justify-space-between ga-3 flex-wrap mb-3">
                    <div class="d-flex align-center ga-2 flex-wrap">
                      <v-chip
                        v-if="row.volcengineCredential.volcenginePlan"
                        size="x-small"
                        color="success"
                        variant="tonal"
                        :href="getVolcenginePlanConsoleURL(row.volcengineCredential.volcenginePlan)"
                        target="_blank"
                        rel="noopener noreferrer"
                        @click.stop
                      >
                        {{ planDisplayName(row.volcengineCredential.volcenginePlan) }}
                        <span v-if="row.volcengineCredential.volcenginePlanTier"> · {{ row.volcengineCredential.volcenginePlanTier }}</span>
                        <v-icon end size="12">mdi-open-in-new</v-icon>
                      </v-chip>
                      <v-chip
                        v-if="row.volcengineCredential.volcengineAccessKeyIdMask"
                        size="x-small"
                        color="info"
                        variant="tonal"
                      >
                        {{ row.volcengineCredential.volcengineAccessKeyIdMask }}
                      </v-chip>
                      <v-chip
                        v-if="!row.volcengineCredential.hasVolcengineAccessKey"
                        size="x-small"
                        color="warning"
                        variant="tonal"
                      >
                        {{ t('volcengineAccessKey.notConfigured') }}
                      </v-chip>
                    </div>
                    <v-btn
                      v-if="row.volcengineCredential.hasVolcengineAccessKey"
                      icon
                      size="x-small"
                      variant="text"
                      :loading="volcengineUsageRefreshing[row.volcengineCredential.credentialUid]"
                      :title="t('volcengineAccessKey.refresh')"
                      @click="refreshVolcengineUsage(row.volcengineCredential)"
                    >
                      <v-icon size="small">mdi-refresh</v-icon>
                    </v-btn>
                  </div>

                  <div v-if="row.volcengineCredential.hasVolcengineAccessKey" class="mb-3">
                    <div
                      v-if="hasVolcengineUsageData(row.volcengineCredential.volcenginePlanUsage)"
                      class="volcengine-usage-grid"
                    >
                      <div
                        v-for="win in volcengineUsageWindows(row.volcengineCredential.volcenginePlanUsage)"
                        :key="win.labelKey"
                      >
                        <div class="text-caption text-medium-emphasis">{{ t(win.labelKey) }}</div>
                        <div class="text-body-2 font-weight-medium" :class="win.colorClass">{{ win.text }}</div>
                      </div>
                    </div>
                    <div v-else class="text-caption text-disabled">
                      {{ row.volcengineCredential.volcenginePlanUsage?.error || t('volcengineAccessKey.noUsageData') }}
                    </div>
                    <div
                      v-if="row.volcengineCredential.volcenginePlanUsage?.fetchedAt && !row.volcengineCredential.volcenginePlanUsage.error"
                      class="text-caption text-disabled mt-2"
                    >
                      {{ t('volcengineAccessKey.usageFetchedAt') }} {{ formatVolcengineTime(row.volcengineCredential.volcenginePlanUsage.fetchedAt) }}
                    </div>
                  </div>

                  <div v-if="volcengineForms[row.volcengineCredential.credentialUid]" class="d-flex flex-column ga-2">
                    <div class="volcengine-key-fields">
                      <v-text-field
                        v-model="volcengineForms[row.volcengineCredential.credentialUid].accessKeyId"
                        :label="t('volcengineAccessKey.accessKeyId')"
                        variant="outlined"
                        density="compact"
                        autocomplete="off"
                        hide-details
                      />
                      <v-text-field
                        v-model="volcengineForms[row.volcengineCredential.credentialUid].secretAccessKey"
                        :label="t('volcengineAccessKey.secretAccessKey')"
                        type="password"
                        variant="outlined"
                        density="compact"
                        autocomplete="new-password"
                        hide-details
                      />
                    </div>
                    <v-alert
                      v-if="volcengineForms[row.volcengineCredential.credentialUid].error"
                      color="error"
                      variant="tonal"
                      density="compact"
                    >
                      {{ volcengineForms[row.volcengineCredential.credentialUid].error }}
                    </v-alert>
                    <div class="d-flex align-center justify-end ga-2">
                      <v-btn
                        v-if="row.volcengineCredential.hasVolcengineAccessKey"
                        size="small"
                        variant="text"
                        color="error"
                        :loading="volcengineForms[row.volcengineCredential.credentialUid].clearing"
                        @click="clearVolcengineAccessKey(row.volcengineCredential)"
                      >
                        <v-icon start size="small">mdi-delete-outline</v-icon>
                        {{ t('volcengineAccessKey.clear') }}
                      </v-btn>
                      <v-btn
                        size="small"
                        variant="tonal"
                        color="primary"
                        :loading="volcengineForms[row.volcengineCredential.credentialUid].saving"
                        :disabled="!canSaveVolcengineCredential(row.volcengineCredential.credentialUid)"
                        @click="saveVolcengineAccessKey(row.volcengineCredential)"
                      >
                        <v-icon start size="small">mdi-content-save-outline</v-icon>
                        {{ t('volcengineAccessKey.verifyAndSave') }}
                      </v-btn>
                    </div>
                  </div>
                </div>
              </v-expand-transition>

              <v-expand-transition>
                <div
                  v-if="row.kimiCredential && expandedCredentialKey === row.key"
                  class="volcengine-key-detail px-4 pt-3 pb-4"
                >
                  <div class="d-flex align-center justify-space-between ga-3 flex-wrap mb-3">
                    <v-chip
                      :color="row.kimiCredential.hasKimiConsoleToken ? 'info' : 'warning'"
                      size="x-small"
                      variant="tonal"
                    >
                      {{ row.kimiCredential.hasKimiConsoleToken
                        ? t('kimiConsoleToken.configured')
                        : t('kimiConsoleToken.notConfigured') }}
                    </v-chip>
                    <v-btn
                      v-if="row.kimiCredential.hasKimiConsoleToken"
                      icon
                      size="x-small"
                      variant="text"
                      :loading="kimiForms[row.kimiCredential.credentialUid]?.refreshing"
                      :title="t('kimiConsoleToken.refresh')"
                      @click="refreshKimiToken(row.kimiCredential)"
                    >
                      <v-icon size="small">mdi-refresh</v-icon>
                    </v-btn>
                  </div>

                  <template v-if="row.kimiCredential.kimiCodeUsage">
                    <div v-if="buildKimiUsageSections(row.kimiCredential.kimiCodeUsage).length" class="kimi-plan-usage mb-3">
                      <div
                        v-for="section in buildKimiUsageSections(row.kimiCredential.kimiCodeUsage)"
                        :key="section.key"
                        class="kimi-plan-usage-row"
                      >
                        <span class="text-body-2 text-medium-emphasis">{{ t(section.labelKey) }}</span>

                        <v-progress-linear
                          v-if="section.kind === 'window'"
                          :model-value="section.usedPercent"
                          :color="kimiUsageColor(section.usedPercent)"
                          height="6"
                          rounded
                        />
                        <div v-else-if="section.kind === 'subscription' || section.kind === 'gift'" class="kimi-balance-stack">
                          <span
                            class="kimi-balance-stack__kimi"
                            :style="{ width: `${section.kimiUsedPercent}%` }"
                          ></span>
                          <span
                            class="kimi-balance-stack__code"
                            :style="{ width: `${section.codeUsedPercent}%` }"
                          ></span>
                        </div>
                        <v-progress-linear
                          v-else-if="section.kind === 'booster' && section.usedPercent !== undefined"
                          :model-value="section.usedPercent"
                          :color="kimiUsageColor(section.usedPercent)"
                          height="6"
                          rounded
                        />
                        <span v-else></span>

                        <span class="text-body-2 font-weight-medium text-no-wrap">
                          <template v-if="section.kind === 'booster'">
                            <template v-if="!section.wallet.allowTopup">{{ t('kimiConsoleToken.boosterWalletDisabled') }}</template>
                            <template v-else>{{ kimiFormatMoney(section.wallet.moneyLeft) }} / {{ kimiFormatMoney(section.wallet.moneyTotal) }}</template>
                          </template>
                          <!-- Kimi 通用额度未消耗时省略 Kimi/Code 拆分，避免与“已用”重复 -->
                          <template v-else-if="(section.kind === 'subscription' || section.kind === 'gift') && section.kimiUsedPercent >= 1">
                            Kimi {{ Math.round(section.kimiUsedPercent) }}% · Code {{ Math.round(section.codeUsedPercent) }}% ·
                            {{ t('kimiConsoleToken.percentUsed', { percent: Math.round(section.usedPercent) }) }}
                          </template>
                          <template v-else>
                            {{ t('kimiConsoleToken.percentUsed', { percent: Math.round(section.usedPercent ?? 0) }) }}
                          </template>
                        </span>

                        <span class="text-caption text-disabled text-no-wrap">
                          <template v-if="section.kind === 'window'">
                            <span :title="kimiFormatDateTime(section.resetTime)">
                              {{ t('kimiConsoleToken.resetsIn', { duration: kimiFormatCountdown(section.resetTime) }) }}
                            </span>
                          </template>
                          <template v-else-if="section.kind === 'subscription' || section.kind === 'gift'">
                            {{ t('kimiConsoleToken.expiresAt') }} {{ kimiFormatExpireTime(section.balance.expireTime) }}
                          </template>
                          <template v-else-if="section.kind === 'booster' && section.wallet.allowTopup">
                            {{ t('kimiConsoleToken.boosterWalletMonthlyUsed') }} {{ kimiFormatMoney(section.wallet.monthlyUsed) }}
                          </template>
                        </span>
                      </div>
                    </div>
                    <div v-else class="text-caption text-disabled mb-3">{{ t('kimiConsoleToken.noUsageData') }}</div>

                    <div class="text-caption text-disabled mb-3">
                      {{ t('kimiConsoleToken.validatedAt') }} {{ kimiFormatDateTime(row.kimiCredential.kimiCodeUsage.validatedAt) }}
                    </div>
                  </template>
                  <div v-else class="text-caption text-disabled mb-3">{{ t('kimiConsoleToken.noUsageData') }}</div>

                  <div v-if="kimiForms[row.kimiCredential.credentialUid]" class="d-flex flex-column ga-2">
                    <v-text-field
                      v-model="kimiForms[row.kimiCredential.credentialUid].accessToken"
                      :label="t('kimiConsoleToken.token')"
                      :placeholder="t('kimiConsoleToken.tokenPlaceholder')"
                      type="password"
                      variant="outlined"
                      density="compact"
                      autocomplete="new-password"
                      hide-details
                    />
                    <v-alert
                      v-if="kimiForms[row.kimiCredential.credentialUid].error"
                      color="error"
                      variant="tonal"
                      density="compact"
                    >
                      {{ kimiForms[row.kimiCredential.credentialUid].error }}
                    </v-alert>
                    <div class="d-flex align-center justify-end ga-2 flex-wrap">
                      <v-btn
                        v-if="row.kimiCredential.hasKimiConsoleToken"
                        size="small"
                        variant="text"
                        color="error"
                        :loading="kimiForms[row.kimiCredential.credentialUid].clearing"
                        @click="clearKimiToken(row.kimiCredential)"
                      >
                        <v-icon start size="small">mdi-delete-outline</v-icon>
                        {{ t('kimiConsoleToken.clear') }}
                      </v-btn>
                      <v-btn
                        size="small"
                        variant="tonal"
                        color="primary"
                        :loading="kimiForms[row.kimiCredential.credentialUid].saving"
                        :disabled="!kimiForms[row.kimiCredential.credentialUid].accessToken.trim()"
                        @click="saveKimiToken(row.kimiCredential)"
                      >
                        <v-icon start size="small">mdi-check-decagram-outline</v-icon>
                        {{ t('kimiConsoleToken.verifyAndSave') }}
                      </v-btn>
                    </div>
                  </div>
                </div>
              </v-expand-transition>

              <v-expand-transition>
                <div
                  v-if="row.mimoCredential && expandedCredentialKey === row.key"
                  class="volcengine-key-detail px-4 pt-3 pb-4"
                >
                  <div class="d-flex align-center ga-2 flex-wrap mb-3">
                    <v-chip v-if="row.mimoCredential.mimoTokenPlan" size="x-small" color="success" variant="tonal">
                      {{ row.mimoCredential.mimoTokenPlan.planName }}
                    </v-chip>
                    <v-chip
                      :color="row.mimoCredential.hasMiMoConsoleCookie ? 'info' : 'warning'"
                      size="x-small"
                      variant="tonal"
                    >
                      {{ row.mimoCredential.hasMiMoConsoleCookie
                        ? t('mimoConsoleCookie.configured')
                        : t('mimoConsoleCookie.notConfigured') }}
                    </v-chip>
                  </div>
                  <div v-if="row.mimoCredential.mimoTokenPlan" class="mimo-usage-grid mb-3">
                    <div>
                      <div class="text-caption text-medium-emphasis">{{ t('mimoConsoleCookie.currentRemaining') }}</div>
                      <div class="text-body-2 font-weight-medium">{{ formatMiMoQuota(row.mimoCredential.mimoTokenPlan.currentUsage) }}</div>
                    </div>
                    <div>
                      <div class="text-caption text-medium-emphasis">{{ t('mimoConsoleCookie.monthRemaining') }}</div>
                      <div class="text-body-2 font-weight-medium">{{ formatMiMoQuota(row.mimoCredential.mimoTokenPlan.monthUsage) }}</div>
                    </div>
                    <div>
                      <div class="text-caption text-medium-emphasis">{{ t('mimoConsoleCookie.expiresAt') }}</div>
                      <div class="text-body-2 font-weight-medium">{{ row.mimoCredential.mimoTokenPlan.currentPeriodEnd }}</div>
                    </div>
                  </div>
                  <div v-else class="text-caption text-disabled mb-3">{{ t('mimoConsoleCookie.noUsageData') }}</div>
                  <div v-if="mimoForms[row.mimoCredential.credentialUid]" class="d-flex flex-column ga-2">
                    <v-text-field
                      v-model="mimoForms[row.mimoCredential.credentialUid].cookie"
                      :label="t('mimoConsoleCookie.cookie')"
                      :placeholder="t('mimoConsoleCookie.cookiePlaceholder')"
                      type="password"
                      variant="outlined"
                      density="compact"
                      autocomplete="new-password"
                      hide-details
                    />
                    <v-alert
                      v-if="mimoForms[row.mimoCredential.credentialUid].error"
                      color="error"
                      variant="tonal"
                      density="compact"
                    >
                      {{ mimoForms[row.mimoCredential.credentialUid].error }}
                    </v-alert>
                    <div class="d-flex align-center justify-end ga-2 flex-wrap">
                      <v-btn
                        v-if="row.mimoCredential.hasMiMoConsoleCookie"
                        size="small"
                        variant="text"
                        color="secondary"
                        :loading="mimoForms[row.mimoCredential.credentialUid].refreshing"
                        @click="refreshMiMoConsoleCookie(row.mimoCredential)"
                      >
                        <v-icon start size="small">mdi-refresh</v-icon>
                        {{ t('mimoConsoleCookie.refresh') }}
                      </v-btn>
                      <v-btn
                        v-if="row.mimoCredential.hasMiMoConsoleCookie"
                        size="small"
                        variant="text"
                        color="error"
                        :loading="mimoForms[row.mimoCredential.credentialUid].clearing"
                        @click="clearMiMoConsoleCookie(row.mimoCredential)"
                      >
                        <v-icon start size="small">mdi-delete-outline</v-icon>
                        {{ t('mimoConsoleCookie.clear') }}
                      </v-btn>
                      <v-btn
                        size="small"
                        variant="tonal"
                        color="primary"
                        :loading="mimoForms[row.mimoCredential.credentialUid].saving"
                        :disabled="!mimoForms[row.mimoCredential.credentialUid].cookie.trim()"
                        @click="saveMiMoConsoleCookie(row.mimoCredential)"
                      >
                        <v-icon start size="small">mdi-check-decagram-outline</v-icon>
                        {{ t('mimoConsoleCookie.verifyAndSave') }}
                      </v-btn>
                    </div>
                  </div>
                </div>
              </v-expand-transition>

              <v-expand-transition>
                <div
                  v-if="row.compshareCredential && expandedCredentialKey === row.key"
                  class="volcengine-key-detail px-4 pt-3 pb-4"
                >
                  <div class="d-flex align-center justify-space-between ga-3 flex-wrap mb-3">
                    <div class="d-flex align-center ga-2 flex-wrap">
                      <v-chip
                        v-if="row.compshareCredential.compsharePlan"
                        :color="row.compshareCredential.compsharePlan.status === 1 ? 'success' : 'error'"
                        size="x-small"
                        variant="tonal"
                      >
                        {{ compsharePlanDisplayName(row.compshareCredential.compsharePlan) }}
                      </v-chip>
                      <v-chip
                        :color="row.compshareCredential.hasCompshareConsoleCookie ? 'info' : 'warning'"
                        size="x-small"
                        variant="tonal"
                      >
                        {{ row.compshareCredential.hasCompshareConsoleCookie
                          ? t('compshareConsoleCookie.configured')
                          : t('compshareConsoleCookie.notConfigured') }}
                      </v-chip>
                    </div>
                    <v-btn
                      v-if="row.compshareCredential.hasCompshareConsoleCookie"
                      icon
                      size="x-small"
                      variant="text"
                      :loading="compshareForms[row.compshareCredential.credentialUid]?.refreshing"
                      :title="t('compshareConsoleCookie.refresh')"
                      @click="refreshCompshareCookie(row.compshareCredential)"
                    >
                      <v-icon size="small">mdi-refresh</v-icon>
                    </v-btn>
                  </div>

                  <template v-if="row.compshareCredential.compsharePlan">
                    <div class="compshare-usage-grid mb-3">
                      <div
                        v-for="item in compshareUsageItems(row.compshareCredential.compsharePlan)"
                        :key="item.label"
                        class="compshare-usage-item"
                      >
                        <div class="text-caption text-medium-emphasis">{{ t(item.label) }}</div>
                        <div class="text-body-2 font-weight-medium">{{ compshareFormatRemaining(item.window) }}</div>
                        <v-progress-linear
                          :model-value="compshareUsagePercent(item.window)"
                          :color="compshareUsageColor(item.window)"
                          height="4"
                          rounded
                          class="my-2"
                        />
                        <div class="text-caption text-disabled">
                          {{ t('compshareConsoleCookie.nextReset') }} {{ compshareFormatEpoch(item.window.nextResetAt) }}
                        </div>
                      </div>
                    </div>
                    <div class="compshare-plan-meta mb-2">
                      <div>
                        <div class="text-caption text-medium-emphasis">{{ t('compshareConsoleCookie.concurrency') }}</div>
                        <div class="text-body-2 font-weight-medium">{{ row.compshareCredential.compsharePlan.concurrencyLimit }}</div>
                      </div>
                      <div>
                        <div class="text-caption text-medium-emphasis">{{ t('compshareConsoleCookie.accountType') }}</div>
                        <div class="text-body-2 font-weight-medium">
                          {{ row.compshareCredential.compsharePlan.isTeam
                            ? t('compshareConsoleCookie.team')
                            : t('compshareConsoleCookie.personal') }}
                        </div>
                      </div>
                      <div>
                        <div class="text-caption text-medium-emphasis">{{ t('compshareConsoleCookie.expiresAt') }}</div>
                        <div class="text-body-2 font-weight-medium">{{ compshareFormatEpoch(row.compshareCredential.compsharePlan.expireAt) }}</div>
                      </div>
                    </div>
                    <div class="text-caption text-disabled mb-3">
                      {{ t('compshareConsoleCookie.validatedAt') }} {{ compshareFormatDateTime(row.compshareCredential.compsharePlan.validatedAt) }}
                    </div>
                  </template>
                  <div v-else class="text-caption text-disabled mb-3">{{ t('compshareConsoleCookie.noUsageData') }}</div>

                  <div v-if="compshareForms[row.compshareCredential.credentialUid]" class="d-flex flex-column ga-2">
                    <v-text-field
                      v-model="compshareForms[row.compshareCredential.credentialUid].cookie"
                      :label="t('compshareConsoleCookie.cookie')"
                      :placeholder="t('compshareConsoleCookie.cookiePlaceholder')"
                      type="password"
                      variant="outlined"
                      density="compact"
                      autocomplete="new-password"
                      hide-details
                    />
                    <v-alert
                      v-if="compshareForms[row.compshareCredential.credentialUid].error"
                      color="error"
                      variant="tonal"
                      density="compact"
                    >
                      {{ compshareForms[row.compshareCredential.credentialUid].error }}
                    </v-alert>
                    <div class="d-flex align-center justify-end ga-2 flex-wrap">
                      <v-btn
                        v-if="row.compshareCredential.hasCompshareConsoleCookie"
                        size="small"
                        variant="text"
                        color="error"
                        :loading="compshareForms[row.compshareCredential.credentialUid].clearing"
                        @click="clearCompshareCookie(row.compshareCredential)"
                      >
                        <v-icon start size="small">mdi-delete-outline</v-icon>
                        {{ t('compshareConsoleCookie.clear') }}
                      </v-btn>
                      <v-btn
                        size="small"
                        variant="tonal"
                        color="primary"
                        :loading="compshareForms[row.compshareCredential.credentialUid].saving"
                        :disabled="!compshareForms[row.compshareCredential.credentialUid].cookie.trim()"
                        @click="saveCompshareCookie(row.compshareCredential)"
                      >
                        <v-icon start size="small">mdi-check-decagram-outline</v-icon>
                        {{ t('compshareConsoleCookie.verifyAndSave') }}
                      </v-btn>
                    </div>
                  </div>
                </div>
              </v-expand-transition>

              <v-expand-transition>
                <div
                  v-if="row.minimaxEndpoint && expandedCredentialKey === row.key"
                  class="volcengine-key-detail px-4 pt-3 pb-4"
                >
                  <div class="d-flex align-center justify-space-between ga-3 flex-wrap mb-3">
                    <v-chip size="x-small" color="primary" variant="tonal">Token Plan</v-chip>
                    <v-btn
                      icon
                      size="x-small"
                      variant="text"
                      :loading="minimaxUsageRefreshing[row.minimaxEndpoint.endpointUid]"
                      :title="t('healthCenter.detail.refreshUsage')"
                      @click="refreshMinimaxUsage(row.minimaxEndpoint)"
                    >
                      <v-icon size="small">mdi-refresh</v-icon>
                    </v-btn>
                  </div>

                  <div
                    v-if="row.minimaxEndpoint.miniMaxTokenPlanUsage?.models.length && !row.minimaxEndpoint.miniMaxTokenPlanUsageError"
                    class="mb-2"
                  >
                    <v-table density="compact">
                      <thead>
                        <tr>
                          <th>{{ t('healthCenter.detail.model') }}</th>
                          <th>{{ t('healthCenter.detail.currentWindow') }}</th>
                          <th>{{ t('healthCenter.detail.weeklyWindow') }}</th>
                          <th>{{ t('healthCenter.detail.resetsIn') }}</th>
                        </tr>
                      </thead>
                      <tbody>
                        <tr v-for="quota in row.minimaxEndpoint.miniMaxTokenPlanUsage.models" :key="quota.modelName">
                          <td class="font-weight-medium">{{ quota.modelName }}</td>
                          <td :class="minimaxQuotaColor(quota.currentIntervalRemainingPercent)">
                            {{ minimaxFormatQuota(quota.currentIntervalRemainingPercent, quota.currentIntervalUsageCount, quota.currentIntervalTotalCount) }}
                          </td>
                          <td :class="minimaxQuotaColor(quota.currentWeeklyRemainingPercent)">
                            {{ minimaxFormatQuota(quota.currentWeeklyRemainingPercent, quota.currentWeeklyUsageCount, quota.currentWeeklyTotalCount) }}
                          </td>
                          <td>{{ minimaxFormatRemainsTime(quota.remainsTimeMs) }}</td>
                        </tr>
                      </tbody>
                    </v-table>
                    <div class="text-caption text-disabled mt-2">
                      {{ t('healthCenter.detail.updatedAt') }}: {{ minimaxFormatDateTime(row.minimaxEndpoint.miniMaxTokenPlanUsage.fetchedAt) }}
                    </div>
                  </div>
                  <div v-else-if="row.minimaxEndpoint.miniMaxTokenPlanUsageError" class="text-caption text-error">
                    {{ t('healthCenter.detail.tokenPlanUsageError') }}: {{ row.minimaxEndpoint.miniMaxTokenPlanUsageError }}
                  </div>
                  <div v-else class="text-caption text-disabled">{{ t('healthCenter.detail.noUsageData') }}</div>
                </div>
              </v-expand-transition>
            </div>
          </v-list>
        </div>

        <div v-if="providerId === 'deepseek' && accountUid" class="deepseek-balance mb-5">
          <v-divider class="mb-4" />
          <div class="d-flex align-center justify-space-between ga-3 flex-wrap mb-2">
            <div class="d-flex align-center ga-2">
              <v-icon color="primary" size="small">mdi-wallet-outline</v-icon>
              <span class="text-body-2 font-weight-medium">{{ t('deepseekBalance.title') }}</span>
            </div>
            <v-btn
              icon
              size="small"
              variant="text"
              color="primary"
              :loading="deepseekBalancesLoading"
              :title="t('deepseekBalance.refresh')"
              @click="loadDeepSeekBalances"
            >
              <v-icon size="small">mdi-refresh</v-icon>
            </v-btn>
          </div>
          <div class="text-caption text-medium-emphasis mb-3">{{ t('deepseekBalance.hint') }}</div>
          <v-progress-linear v-if="deepseekBalancesLoading" indeterminate color="primary" class="mb-3" />
          <v-alert v-if="deepseekBalancesError" color="error" variant="tonal" density="compact" class="mb-3">
            {{ deepseekBalancesError }}
          </v-alert>
          <div
            v-for="credential in deepseekBalances"
            :key="credential.credentialUid"
            class="deepseek-credential py-3"
          >
            <div class="d-flex align-center justify-space-between ga-3 flex-wrap mb-2">
              <code class="text-caption">{{ credential.keyMask }}</code>
              <v-chip
                size="x-small"
                variant="tonal"
                :color="credential.error ? 'error' : credential.isAvailable ? 'success' : 'warning'"
              >
                {{ credential.error
                  ? t('deepseekBalance.queryFailed')
                  : credential.isAvailable
                    ? t('deepseekBalance.available')
                    : t('deepseekBalance.unavailable') }}
              </v-chip>
            </div>
            <v-alert v-if="credential.error" color="error" variant="tonal" density="compact">
              {{ credential.error }}
            </v-alert>
            <div v-else-if="credential.balanceInfos?.length" class="deepseek-balance-grid">
              <div v-for="balance in credential.balanceInfos" :key="balance.currency" class="deepseek-balance-currency">
                <div class="text-caption font-weight-medium mb-2">{{ balance.currency }}</div>
                <div class="deepseek-balance-values">
                  <div>
                    <div class="text-caption text-medium-emphasis">{{ t('deepseekBalance.total') }}</div>
                    <div class="text-body-2 font-weight-medium">{{ balance.totalBalance }}</div>
                  </div>
                  <div>
                    <div class="text-caption text-medium-emphasis">{{ t('deepseekBalance.granted') }}</div>
                    <div class="text-body-2 font-weight-medium">{{ balance.grantedBalance }}</div>
                  </div>
                  <div>
                    <div class="text-caption text-medium-emphasis">{{ t('deepseekBalance.toppedUp') }}</div>
                    <div class="text-body-2 font-weight-medium">{{ balance.toppedUpBalance }}</div>
                  </div>
                </div>
              </div>
            </div>
            <div v-else class="text-caption text-disabled">{{ t('deepseekBalance.noBalance') }}</div>
          </div>
        </div>

        <!-- 添加新密钥 -->
        <div class="d-flex align-start ga-3">
          <v-text-field
            v-model="newApiKey"
            :label="t('addChannel.addNewApiKey')"
            :placeholder="t('channelEditor.auth.addNewApiKey.placeholder')"
            prepend-inner-icon="mdi-plus"
            variant="outlined"
            density="comfortable"
            type="password"
            :error="!!apiKeyError"
            :error-messages="apiKeyError"
            class="flex-grow-1"
            @keyup.enter="handleAddKey"
            @input="handleInput"
          />
          <v-btn
            color="primary"
            variant="elevated"
            size="large"
            height="40"
            :disabled="!newApiKey.trim()"
            class="mt-1"
            @click="handleAddKey"
          >
            {{ t('app.actions.add') }}
          </v-btn>
        </div>

        <v-alert
          v-if="serviceType === 'copilot'"
          class="mt-3"
          color="info"
          variant="tonal"
          density="comfortable"
        >
          <div class="d-flex flex-column ga-2">
            <div class="text-body-2">{{ t('copilotOAuth.description') }}</div>
            <v-text-field
              :model-value="proxyUrl || ''"
              :label="t('channelEditor.transport.proxyUrl.label')"
              :placeholder="t('channelEditor.transport.proxyUrl.placeholder')"
              :hint="t('channelEditor.transport.proxyUrl.hint')"
              prepend-inner-icon="mdi-shield-lock-outline"
              variant="outlined"
              density="compact"
              clearable
              persistent-hint
              @update:model-value="$emit('update:proxyUrl', String($event || ''))"
            />
            <div v-if="copilotUserCode" class="d-flex align-center flex-wrap ga-2">
              <span class="text-body-2">{{ t('copilotOAuth.userCode') }}</span>
              <code class="px-2 py-1 rounded bg-surface">{{ copilotUserCode }}</code>
              <v-btn
                size="small"
                color="primary"
                variant="text"
                :href="copilotVerificationUri"
                target="_blank"
                rel="noopener noreferrer"
              >
                <v-icon start size="small">mdi-open-in-new</v-icon>
                {{ t('copilotOAuth.openAuthorize') }}
              </v-btn>
            </div>
            <v-alert v-if="copilotOAuthSuccess" color="success" variant="tonal" density="compact">
              {{ t('copilotOAuth.success') }}
            </v-alert>
            <v-alert v-if="copilotOAuthError" color="error" variant="tonal" density="compact">
              {{ copilotOAuthError }}
            </v-alert>
            <div class="d-flex align-center ga-2">
              <v-btn
                color="primary"
                variant="tonal"
                :loading="copilotOAuthLoading"
                @click="startCopilotOAuth"
              >
                <v-icon start>mdi-code-braces</v-icon>
                {{ t('copilotOAuth.button') }}
              </v-btn>
              <span v-if="copilotPolling" class="text-caption text-medium-emphasis">
                {{ t('copilotOAuth.waiting') }}
              </span>
            </div>
            <div class="d-flex align-center ga-2 mt-1">
              <v-btn
                v-if="copilotPolling || copilotOAuthLoading"
                size="small"
                color="warning"
                variant="text"
                @click="cancelCopilotOAuth"
              >
                {{ t('copilotOAuth.cancel') }}
              </v-btn>
              <v-btn
                v-if="copilotOAuthError"
                size="small"
                color="primary"
                variant="text"
                @click="retryCopilotOAuth"
              >
                {{ t('copilotOAuth.retry') }}
              </v-btn>
            </div>
            <div v-if="serviceType === 'copilot' && isEditing && channelId !== undefined" class="mt-3 d-flex flex-column ga-2">
              <div class="d-flex align-center ga-2">
                <v-btn
                  color="secondary"
                  variant="tonal"
                  :loading="copilotDiagnoseLoading"
                  @click="diagnoseCopilotChannel"
                >
                  {{ t('copilotDiagnose.button') }}
                </v-btn>
                <span v-if="copilotDiagnoseLoading" class="text-caption text-medium-emphasis">
                  {{ t('copilotDiagnose.loading') }}
                </span>
              </div>
              <v-alert v-if="copilotDiagnoseError" color="error" variant="tonal" density="compact">
                {{ copilotDiagnoseError }}
              </v-alert>
              <v-alert v-if="copilotDiagnoseResult" color="info" variant="tonal" density="compact" class="text-caption">
                <div class="d-flex flex-column ga-2">
                  <div class="d-flex align-center ga-2">
                    <v-chip :color="copilotDiagnoseResult.githubUser ? 'success' : 'warning'" size="small" variant="tonal">
                      GitHub
                    </v-chip>
                    <span v-if="copilotDiagnoseResult.githubUser">{{ copilotDiagnoseResult.githubUser.login }}</span>
                    <span v-else>{{ copilotDiagnoseResult.githubUserError }}</span>
                  </div>
                  <div class="d-flex align-center ga-2">
                    <v-chip :color="copilotDiagnoseResult.tokenError ? 'error' : 'success'" size="small" variant="tonal">
                      Token
                    </v-chip>
                    <span v-if="copilotDiagnoseResult.tokenError">{{ copilotDiagnoseResult.tokenError }}</span>
                    <span v-else>{{ copilotDiagnoseResult.copilotBaseUrl }}</span>
                  </div>
                  <div class="d-flex align-center ga-2">
                    <v-chip :color="copilotDiagnoseResult.modelsError ? 'error' : (copilotDiagnoseResult.modelsStatus && copilotDiagnoseResult.modelsStatus < 400 ? 'success' : 'warning')" size="small" variant="tonal">
                      Models
                    </v-chip>
                    <span v-if="copilotDiagnoseResult.modelsError">{{ copilotDiagnoseResult.modelsError }}</span>
                    <span v-else>{{ copilotDiagnoseResult.modelsStatus }} {{ copilotDiagnoseResult.modelsUrl }}</span>
                  </div>
                </div>
              </v-alert>
            </div>
          </div>
        </v-alert>

        <!-- 被限制的 (Key, 模型) 组合（仅编辑模式） -->
        <div v-if="isEditing && visibleDisabledKeyModels.length" class="mt-4">
          <div class="d-flex align-center ga-2 mb-2">
            <v-icon size="small" color="warning">mdi-alert-circle-outline</v-icon>
            <span class="text-body-2 font-weight-medium text-warning">{{ t('channelCard.disabledKeyModels') }}</span>
            <v-chip size="x-small" color="warning" variant="tonal">{{ visibleDisabledKeyModels.length }}</v-chip>
          </div>
          <v-list density="compact" class="rounded-lg" style="max-height: 150px; overflow-y: auto;">
            <v-list-item
              v-for="(dm, dmIdx) in visibleDisabledKeyModels"
              :key="'disabled-model-' + dmIdx"
              class="px-3"
              style="background: rgba(var(--v-theme-warning), 0.04);"
            >
              <template #prepend>
                <v-icon size="small" color="warning" class="mr-2">mdi-key-alert</v-icon>
              </template>
              <v-list-item-title class="text-caption font-weight-mono">
                {{ dm.key.length > 20 ? dm.key.slice(0, 8) + '***' + dm.key.slice(-5) : dm.key }}
                <v-chip size="x-small" color="primary" variant="tonal" class="ml-1">{{ dm.model }}</v-chip>
              </v-list-item-title>
              <v-list-item-subtitle class="d-flex align-center ga-1">
                <v-chip size="x-small" color="warning" variant="tonal">{{ t('channelCard.modelNotFound') }}</v-chip>
                <span class="text-caption">{{ t('channelCard.recoverAt') }}: {{ new Date(dm.recoverAt).toLocaleString() }}</span>
              </v-list-item-subtitle>
              <template #append>
                <v-btn
                  size="x-small"
                  color="success"
                  variant="tonal"
                  rounded="lg"
                  :loading="restoringKeyModel === (dm.key + '|' + dm.model)"
                  :disabled="!!restoringKeyModel"
                  @click="$emit('restore-key-model', dm.key, dm.model)"
                >
                  <v-icon start size="small">mdi-restore</v-icon>
                  {{ t('channelCard.restoreKey') }}
                </v-btn>
              </template>
            </v-list-item>
          </v-list>
        </div>
      </v-card-text>
    </v-card>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onBeforeUnmount, watch } from 'vue'
import { useI18n } from '../../i18n'
import { ApiError, ApiService } from '../../services/api'
import type {
  CompsharePlanSnapshot,
  CompsharePlanUsageWindow,
  DeepSeekCredentialBalance,
  DisabledKeyInfo,
  EndpointDetailItem,
  KimiCodeMoney,
  ManagedAccountCredential,
  MiMoTokenPlanQuota,
  VolcenginePlanUsage,
  VolcenginePlanUsageWindow,
  APIKeyConfig,
} from '../../services/api-types'
import { maskApiKey } from '../../utils/apiKeyMask'
import { buildChannelApiKeyRows, type ChannelApiKeyRow } from '../../utils/channelApiKeys'
import { getVolcenginePlanConsoleURL } from '../../utils/channelWebsite'
import { quotaRemainingColorClass, quotaRemainingColorHex } from '../../utils/quotaColor'
import { buildKimiUsageSections } from '../../utils/kimiPlanUsage'
import { selectMiniMaxTokenPlanEndpoint, sha256KeyHash } from '../../utils/minimaxEndpointUsage'

interface KeyModelsStatus {
  loading?: boolean
  success?: boolean
  error?: string
  statusCode?: string | number
  modelCount?: number
}

interface DisabledKeyModel {
  key: string
  model: string
  reason: string
  disabledAt: string
  recoverAt: string
}

interface Props {
  apiKeys: string[]
  disabledKeys: DisabledKeyInfo[]
  disabledKeyModels?: DisabledKeyModel[]
  apiKeyConfigs?: APIKeyConfig[]
  keyModelsStatus: Map<string, KeyModelsStatus>
  isEditing: boolean
  restoringKey: string
  restoringKeyModel?: string
  removingKey?: string
  suspendingKey?: string
  serviceType?: string
  isAutoManaged?: boolean
  channelId?: number
  channelUid?: string
  dialogOpen: boolean
  proxyUrl?: string
  accountUid?: string
  providerId?: string
}

const props = defineProps<Props>()

const emit = defineEmits<{
  'update:apiKeys': [string[]]
  'update:proxyUrl': [string]
  'restore-key': [string]
  'restore-key-model': [string, string]
  'remove-key': [string]
  'suspend-key': [string]
  'resume-key': [string]
}>()

const { t } = useI18n()
const apiService = new ApiService()

const newApiKey = ref('')
const apiKeyError = ref('')
const duplicateKeyIndex = ref<number | null>(null)
const copiedKey = ref('')
const deepseekBalances = ref<DeepSeekCredentialBalance[]>([])
const deepseekBalancesLoading = ref(false)
const deepseekBalancesError = ref('')
interface VolcengineCredentialForm {
  accessKeyId: string
  secretAccessKey: string
  saving: boolean
  clearing: boolean
  error: string
}

const volcengineCredentials = ref<ManagedAccountCredential[]>([])
const volcengineCredentialsLoading = ref(false)
const volcengineCredentialsError = ref('')
const volcengineForms = ref<Record<string, VolcengineCredentialForm>>({})
const volcengineUsageRefreshing = ref<Record<string, boolean>>({})
const expandedCredentialKey = ref<string | null>(null)
interface KimiCredentialForm {
  accessToken: string
  saving: boolean
  refreshing: boolean
  clearing: boolean
  error: string
}

const kimiCredentials = ref<ManagedAccountCredential[]>([])
const kimiCredentialsLoading = ref(false)
const kimiCredentialsError = ref('')
const kimiForms = ref<Record<string, KimiCredentialForm>>({})
interface MiMoCredentialForm {
  cookie: string
  saving: boolean
  refreshing: boolean
  clearing: boolean
  error: string
}

const mimoCredentials = ref<ManagedAccountCredential[]>([])
const mimoCredentialsLoading = ref(false)
const mimoCredentialsError = ref('')
const mimoForms = ref<Record<string, MiMoCredentialForm>>({})
interface CompshareCredentialForm {
  cookie: string
  saving: boolean
  refreshing: boolean
  clearing: boolean
  error: string
}

const compshareCredentials = ref<ManagedAccountCredential[]>([])
const compshareCredentialsLoading = ref(false)
const compshareCredentialsError = ref('')
const compshareForms = ref<Record<string, CompshareCredentialForm>>({})

const minimaxEndpoints = ref<EndpointDetailItem[]>([])
const minimaxEndpointsLoading = ref(false)
const minimaxEndpointsError = ref('')
const minimaxUsageRefreshing = ref<Record<string, boolean>>({})
const minimaxKeyHashes = ref<Record<string, string>>({})
let minimaxEndpointsRequestId = 0
let minimaxKeyHashRequestId = 0
interface CopilotDiagnoseResponse {
  githubUser?: {
    login?: string
    id?: number
  }
  githubUserError?: string
  copilotBaseUrl?: string
  tokenError?: string
  tokenErrorKind?: string
  modelsUrl?: string
  modelsStatus?: number
  modelsError?: string
  modelsBodyPrefix?: string
}

const copilotOAuthLoading = ref(false)
const copilotPolling = ref(false)
const copilotOAuthError = ref('')
const copilotOAuthSuccess = ref(false)
const copilotDeviceCode = ref('')
const copilotUserCode = ref('')
const copilotVerificationUri = ref('')
const copilotDiagnoseLoading = ref(false)
const copilotDiagnoseResult = ref<CopilotDiagnoseResponse | null>(null)
const copilotDiagnoseError = ref('')
let copilotPollTimer: number | null = null

const keyRows = computed(() => buildChannelApiKeyRows(props.apiKeys, props.disabledKeys, props.apiKeyConfigs).map(row => {
  const matchCredential = (credentials: ManagedAccountCredential[]) =>
    credentials.find(credential => credential.keyMask === maskApiKey(row.key))
  const volcengineCredential = props.providerId === 'volcengine' ? matchCredential(volcengineCredentials.value) : undefined
  const kimiCredential = props.providerId === 'kimi' ? matchCredential(kimiCredentials.value) : undefined
  const mimoCredential = props.providerId === 'mimo' ? matchCredential(mimoCredentials.value) : undefined
  const compshareCredential = props.providerId === 'compshare' ? matchCredential(compshareCredentials.value) : undefined
  const minimaxEndpoint = props.providerId === 'minimax'
    ? selectMiniMaxTokenPlanEndpoint(minimaxEndpoints.value, minimaxKeyHashes.value[row.key] ?? '', maskApiKey(row.key))
    : undefined
  return {
    ...row,
    volcengineCredential,
    kimiCredential,
    mimoCredential,
    compshareCredential,
    minimaxEndpoint,
    planCredential: volcengineCredential ?? kimiCredential ?? mimoCredential ?? compshareCredential,
  }
}))

watch(
  () => buildChannelApiKeyRows(props.apiKeys, props.disabledKeys).map(row => row.key),
  async keys => {
    const requestId = ++minimaxKeyHashRequestId
    const entries = await Promise.all(keys.map(async key => {
      try {
        return [key, await sha256KeyHash(key)] as const
      } catch {
        return [key, ''] as const
      }
    }))
    if (requestId === minimaxKeyHashRequestId) minimaxKeyHashes.value = Object.fromEntries(entries)
  },
  { immediate: true },
)

const planRowTitle = (): string => {
  switch (props.providerId) {
    case 'volcengine': return t('volcengineAccessKey.usageTitle')
    case 'kimi': return t('kimiConsoleToken.title')
    case 'mimo': return t('mimoConsoleCookie.title')
    case 'compshare': return t('compshareConsoleCookie.title')
    case 'minimax': return t('healthCenter.detail.tokenPlanUsage')
    default: return ''
  }
}

const hasConfigurableKeys = computed(() => props.serviceType === 'copilot' || keyRows.value.length > 0)

const visibleDisabledKeyModels = computed(() => props.disabledKeyModels || [])

const toggleCredentialKey = (key: string) => {
  expandedCredentialKey.value = expandedCredentialKey.value === key ? null : key
}

const disabledKeyColor = (reason: string) => (
  reason === 'insufficient_balance' || reason === 'insufficient_quota' ? 'warning' : 'error'
)

const formatDisabledTime = (iso: string) => {
  const date = new Date(iso)
  return Number.isNaN(date.getTime()) ? iso : date.toLocaleString()
}

const loadDeepSeekBalances = async () => {
  deepseekBalances.value = []
  deepseekBalancesError.value = ''
  if (props.providerId !== 'deepseek' || !props.accountUid) return
  deepseekBalancesLoading.value = true
  try {
    const response = await apiService.getDeepSeekAccountBalances(props.accountUid)
    deepseekBalances.value = response.balances
  } catch (err) {
    deepseekBalancesError.value = err instanceof Error ? err.message : String(err)
  } finally {
    deepseekBalancesLoading.value = false
  }
}

watch(
  () => [props.providerId, props.accountUid],
  () => { void loadDeepSeekBalances() },
  { immediate: true }
)

const loadVolcengineCredentials = async () => {
  volcengineCredentials.value = []
  volcengineCredentialsError.value = ''
  if (props.providerId !== 'volcengine' || !props.accountUid) return
  volcengineCredentialsLoading.value = true
  try {
    const response = await apiService.getManagedAccounts()
    const account = response.accounts.find(item => item.accountUid === props.accountUid)
    if (!account) {
      volcengineCredentialsError.value = t('volcengineAccessKey.accountNotFound')
      return
    }
    volcengineCredentials.value = account.credentials
    const nextForms: Record<string, VolcengineCredentialForm> = {}
    for (const credential of account.credentials) {
      nextForms[credential.credentialUid] = volcengineForms.value[credential.credentialUid] ?? {
        accessKeyId: '',
        secretAccessKey: '',
        saving: false,
        clearing: false,
        error: '',
      }
    }
    volcengineForms.value = nextForms
    // 打开编辑框时对已绑定 AK 的凭证自动刷新用量（后端 TTL 兜底避免频繁请求）。
    void Promise.allSettled(
      account.credentials
        .filter(credential => credential.hasVolcengineAccessKey)
        .map(credential => refreshVolcengineUsage(credential))
    )
  } catch (err) {
    volcengineCredentialsError.value = err instanceof Error ? err.message : String(err)
  } finally {
    volcengineCredentialsLoading.value = false
  }
}

watch(
  () => [props.providerId, props.accountUid],
  () => { void loadVolcengineCredentials() },
  { immediate: true }
)

const loadKimiCredentials = async () => {
  kimiCredentials.value = []
  kimiCredentialsError.value = ''
  if (props.providerId !== 'kimi' || !props.accountUid) return
  kimiCredentialsLoading.value = true
  try {
    const response = await apiService.getManagedAccounts()
    const account = response.accounts.find(item => item.accountUid === props.accountUid)
    if (!account) {
      kimiCredentialsError.value = t('kimiConsoleToken.accountNotFound')
      return
    }
    kimiCredentials.value = account.credentials
    const nextForms: Record<string, KimiCredentialForm> = {}
    for (const credential of account.credentials) {
      nextForms[credential.credentialUid] = kimiForms.value[credential.credentialUid] ?? {
        accessToken: '',
        saving: false,
        refreshing: false,
        clearing: false,
        error: '',
      }
    }
    kimiForms.value = nextForms
  } catch (err) {
    kimiCredentialsError.value = err instanceof Error ? err.message : String(err)
  } finally {
    kimiCredentialsLoading.value = false
  }
}

watch(
  () => [props.providerId, props.accountUid],
  () => { void loadKimiCredentials() },
  { immediate: true }
)

const loadMiMoCredentials = async () => {
  mimoCredentials.value = []
  mimoCredentialsError.value = ''
  if (props.providerId !== 'mimo' || !props.accountUid) return
  mimoCredentialsLoading.value = true
  try {
    const response = await apiService.getManagedAccounts()
    const account = response.accounts.find(item => item.accountUid === props.accountUid)
    if (!account) {
      mimoCredentialsError.value = t('mimoConsoleCookie.accountNotFound')
      return
    }
    mimoCredentials.value = account.credentials
    const nextForms: Record<string, MiMoCredentialForm> = {}
    for (const credential of account.credentials) {
      nextForms[credential.credentialUid] = mimoForms.value[credential.credentialUid] ?? {
        cookie: '', saving: false, refreshing: false, clearing: false, error: '',
      }
    }
    mimoForms.value = nextForms
  } catch (err) {
    mimoCredentialsError.value = err instanceof Error ? err.message : String(err)
  } finally {
    mimoCredentialsLoading.value = false
  }
}

watch(
  () => [props.providerId, props.accountUid],
  () => { void loadMiMoCredentials() },
  { immediate: true }
)

const loadCompshareCredentials = async () => {
  compshareCredentials.value = []
  compshareCredentialsError.value = ''
  if (props.providerId !== 'compshare' || !props.accountUid) return
  compshareCredentialsLoading.value = true
  try {
    const response = await apiService.getManagedAccounts()
    const account = response.accounts.find(item => item.accountUid === props.accountUid)
    if (!account) {
      compshareCredentialsError.value = t('compshareConsoleCookie.accountNotFound')
      return
    }
    compshareCredentials.value = account.credentials
    const nextForms: Record<string, CompshareCredentialForm> = {}
    for (const credential of account.credentials) {
      nextForms[credential.credentialUid] = compshareForms.value[credential.credentialUid] ?? {
        cookie: '',
        saving: false,
        refreshing: false,
        clearing: false,
        error: '',
      }
    }
    compshareForms.value = nextForms
  } catch (err) {
    compshareCredentialsError.value = err instanceof Error ? err.message : String(err)
  } finally {
    compshareCredentialsLoading.value = false
  }
}

watch(
  () => [props.providerId, props.accountUid],
  () => { void loadCompshareCredentials() },
  { immediate: true }
)

const loadMiniMaxEndpoints = async (requestId: number, channelUid: string): Promise<boolean> => {
  const response = await apiService.getHealthCenterChannelEndpoints(channelUid)
  if (requestId !== minimaxEndpointsRequestId) return false
  minimaxEndpoints.value = response.endpoints
  minimaxEndpointsError.value = ''
  return true
}

watch(
  () => [props.dialogOpen, props.providerId, props.channelUid] as const,
  async ([dialogOpen, providerId, channelUid]) => {
    const requestId = ++minimaxEndpointsRequestId
    minimaxEndpoints.value = []
    minimaxEndpointsError.value = ''
    minimaxEndpointsLoading.value = false
    minimaxUsageRefreshing.value = {}
    if (!dialogOpen || providerId !== 'minimax' || !channelUid) return

    minimaxEndpointsLoading.value = true
    try {
      if (!await loadMiniMaxEndpoints(requestId, channelUid)) return
      // 后端用 TTL 控制实际请求频率；弹窗每次打开都回读一次最新缓存。
      const supported = minimaxEndpoints.value.filter(endpoint => endpoint.tokenPlanUsageSupported)
      if (supported.length > 0) {
        await Promise.allSettled(supported.map(endpoint => apiService.refreshEndpointTokenPlanUsage(endpoint.endpointUid)))
        if (requestId !== minimaxEndpointsRequestId) return
        await loadMiniMaxEndpoints(requestId, channelUid)
      }
    } catch (err) {
      if (requestId !== minimaxEndpointsRequestId) return
      minimaxEndpoints.value = []
      minimaxEndpointsError.value = err instanceof Error ? err.message : String(err)
    } finally {
      if (requestId === minimaxEndpointsRequestId) minimaxEndpointsLoading.value = false
    }
  },
  { immediate: true },
)

const refreshMinimaxUsage = async (endpoint: EndpointDetailItem) => {
  const requestId = minimaxEndpointsRequestId
  const endpointUid = endpoint.endpointUid
  minimaxUsageRefreshing.value = { ...minimaxUsageRefreshing.value, [endpointUid]: true }
  try {
    const response = await apiService.refreshEndpointTokenPlanUsage(endpointUid)
    if (requestId !== minimaxEndpointsRequestId) return
    const current = minimaxEndpoints.value.find(item => item.endpointUid === endpointUid)
    if (!current) return
    current.miniMaxTokenPlanUsage = response.usage
    current.miniMaxTokenPlanUsageError = ''
  } catch (err) {
    if (requestId !== minimaxEndpointsRequestId) return
    const current = minimaxEndpoints.value.find(item => item.endpointUid === endpointUid)
    if (!current) return
    current.miniMaxTokenPlanUsage = undefined
    current.miniMaxTokenPlanUsageError = err instanceof Error ? err.message : String(err)
  } finally {
    if (requestId === minimaxEndpointsRequestId) {
      minimaxUsageRefreshing.value = { ...minimaxUsageRefreshing.value, [endpointUid]: false }
    }
  }
}

const saveCompshareCookie = async (credential: ManagedAccountCredential) => {
  if (!props.accountUid) return
  const form = compshareForms.value[credential.credentialUid]
  if (!form?.cookie.trim()) return
  form.saving = true
  form.error = ''
  try {
    const response = await apiService.setCompshareConsoleCookie(props.accountUid, credential.credentialUid, form.cookie.trim())
    credential.hasCompshareConsoleCookie = true
    credential.compsharePlan = response.plan
    form.cookie = ''
  } catch (err) {
    form.error = err instanceof Error ? err.message : String(err)
  } finally {
    form.saving = false
  }
}

const refreshCompshareCookie = async (credential: ManagedAccountCredential) => {
  if (!props.accountUid) return
  const form = compshareForms.value[credential.credentialUid]
  if (!form) return
  form.refreshing = true
  form.error = ''
  try {
    const response = await apiService.refreshCompshareConsoleCookie(props.accountUid, credential.credentialUid)
    credential.compsharePlan = response.plan
  } catch (err) {
    form.error = err instanceof Error ? err.message : String(err)
  } finally {
    form.refreshing = false
  }
}

const clearCompshareCookie = async (credential: ManagedAccountCredential) => {
  if (!props.accountUid || !window.confirm(t('compshareConsoleCookie.clearConfirm'))) return
  const form = compshareForms.value[credential.credentialUid]
  if (!form) return
  form.clearing = true
  form.error = ''
  try {
    await apiService.clearCompshareConsoleCookie(props.accountUid, credential.credentialUid)
    credential.hasCompshareConsoleCookie = false
    credential.compsharePlan = undefined
  } catch (err) {
    form.error = err instanceof Error ? err.message : String(err)
  } finally {
    form.clearing = false
  }
}

const compshareUsageItems = (plan: CompsharePlanSnapshot) => [
  { label: 'compshareConsoleCookie.fiveHourRemaining', window: plan.fiveHourUsage },
  { label: 'compshareConsoleCookie.weeklyRemaining', window: plan.weeklyUsage },
  { label: 'compshareConsoleCookie.monthlyRemaining', window: plan.monthlyUsage },
]

const compshareNumberFormat = new Intl.NumberFormat()
const compshareDateTimeFormat = new Intl.DateTimeFormat(undefined, {
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
})

const compshareFormatRemaining = (window: CompsharePlanUsageWindow) => {
  const remaining = Math.max(0, window.limit - window.used)
  const remainingPercent = window.limit > 0 ? ((remaining / window.limit) * 100).toFixed(1) : '0.0'
  return `${t('volcengineAccessKey.remaining')} ${remainingPercent}% · ${compshareNumberFormat.format(remaining)}/${compshareNumberFormat.format(window.limit)}`
}

const compshareUsagePercent = (window: CompsharePlanUsageWindow) => {
  if (window.limit <= 0) return 0
  return Math.max(0, Math.min(100, (window.used / window.limit) * 100))
}

// Compshare 传入的是"已用百分比"，进度条 color 属性需要 Vuetify 色名或 hex。
const compshareUsageColor = (window: CompsharePlanUsageWindow) => {
  return quotaRemainingColorHex(100 - compshareUsagePercent(window))
}

const compshareFormatEpoch = (value?: number) =>
  value && value > 0 ? compshareDateTimeFormat.format(new Date(value * 1000)) : '-'

const compshareFormatDateTime = (value?: string) => {
  if (!value) return '-'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '-' : compshareDateTimeFormat.format(date)
}

const compsharePlanDisplayName = (plan: CompsharePlanSnapshot) => plan.displayName || plan.planName || plan.planCode

// Key 行摘要：未绑定 Cookie 时提示绑定，否则展示套餐名与 5 小时/本周/月度余量。
const compshareUsageSummary = (credential: ManagedAccountCredential): string => {
  if (!credential.hasCompshareConsoleCookie) return t('compshareConsoleCookie.notConfigured')
  const plan = credential.compsharePlan
  if (!plan) return t('compshareConsoleCookie.noUsageData')
  const parts = [compsharePlanDisplayName(plan)]
  if (plan.fiveHourUsage.limit > 0) {
    parts.push(`${t('compshareConsoleCookie.fiveHourRemaining')} ${compshareFormatRemaining(plan.fiveHourUsage)}`)
  }
  if (plan.weeklyUsage.limit > 0) {
    parts.push(`${t('compshareConsoleCookie.weeklyRemaining')} ${compshareFormatRemaining(plan.weeklyUsage)}`)
  }
  if (plan.monthlyUsage.limit > 0) {
    parts.push(`${t('compshareConsoleCookie.monthlyRemaining')} ${compshareFormatRemaining(plan.monthlyUsage)}`)
  }
  return parts.join(' · ')
}

// Key 行摘要：未绑定 Cookie 时提示绑定，否则展示套餐名与当前/月度余量。
const mimoUsageSummary = (credential: ManagedAccountCredential): string => {
  if (!credential.hasMiMoConsoleCookie) return t('mimoConsoleCookie.notConfigured')
  const plan = credential.mimoTokenPlan
  if (!plan) return t('mimoConsoleCookie.noUsageData')
  const parts = [
    plan.planName,
    `${t('mimoConsoleCookie.currentRemaining')} ${formatMiMoQuota(plan.currentUsage)}`,
  ]
  if (plan.monthUsage.limit > 0) {
    parts.push(`${t('mimoConsoleCookie.monthRemaining')} ${formatMiMoQuota(plan.monthUsage)}`)
  }
  return parts.join(' · ')
}

const minimaxFormatQuota = (remainingPercent: number, used: number, total: number) => {
  const percent = Math.max(0, Math.min(100, remainingPercent)).toFixed(0)
  return total > 0 ? `剩余 ${percent}% (${used}/${total})` : `剩余 ${percent}%`
}

// MiniMax 传入的是"剩余百分比"，:class 绑定表格单元格文字颜色，需要 scoped CSS 类名。
const minimaxQuotaColor = (remainingPercent: number) => {
  return quotaRemainingColorClass(remainingPercent)
}

const minimaxFormatRemainsTime = (milliseconds: number) => {
  if (milliseconds <= 0) return t('healthCenter.detail.resetSoon')
  const minutes = Math.floor(milliseconds / 60000)
  const hours = Math.floor(minutes / 60)
  const remainingMinutes = minutes % 60
  return hours > 0 ? `${hours}h ${remainingMinutes}m` : `${remainingMinutes}m`
}

const minimaxFormatDateTime = (value?: string) => {
  if (!value) return '-'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '-' : date.toLocaleString()
}

// Key 行摘要：展示各模型当前窗口剩余百分比。
const minimaxUsageSummary = (endpoint: EndpointDetailItem): string => {
  if (endpoint.miniMaxTokenPlanUsageError) return endpoint.miniMaxTokenPlanUsageError
  const usage = endpoint.miniMaxTokenPlanUsage
  if (!usage?.models.length) return t('healthCenter.detail.noUsageData')
  return usage.models
    .map(model => `${model.modelName} 剩余 ${Math.max(0, Math.min(100, model.currentIntervalRemainingPercent)).toFixed(0)}%`)
    .join(' · ')
}

const applyMiMoCookieResponse = (credential: ManagedAccountCredential, response: Awaited<ReturnType<ApiService['setMiMoConsoleCookie']>>) => {
  if (response.keyAdopted && response.adoptedApiKey) {
    const keyIndex = props.apiKeys.findIndex(key => maskApiKey(key) === credential.keyMask)
    if (keyIndex >= 0) {
      const updated = [...props.apiKeys]
      updated[keyIndex] = response.adoptedApiKey
      emit('update:apiKeys', updated)
    }
    credential.keyMask = response.keyMask
  }
  credential.hasMiMoConsoleCookie = true
  credential.mimoTokenPlan = response.tokenPlan
  mimoForms.value[credential.credentialUid].cookie = ''
}

const bindMiMoConsoleCookie = async (credential: ManagedAccountCredential, adoptCookieKey: boolean) => {
  if (!props.accountUid) return
  const form = mimoForms.value[credential.credentialUid]
  const response = await apiService.setMiMoConsoleCookie(props.accountUid, credential.credentialUid, {
    cookie: form.cookie.trim(),
    adoptCookieKey,
  })
  applyMiMoCookieResponse(credential, response)
}

const saveMiMoConsoleCookie = async (credential: ManagedAccountCredential) => {
  const form = mimoForms.value[credential.credentialUid]
  if (!form?.cookie.trim()) return
  form.saving = true
  form.error = ''
  try {
    await bindMiMoConsoleCookie(credential, false)
  } catch (err) {
    const details = err instanceof ApiError && typeof err.details === 'object' && err.details
      ? err.details as { code?: string; currentKeyMask?: string; cookieKeyMask?: string }
      : null
    if (err instanceof ApiError && err.status === 409 && details?.code === 'mimo_cookie_key_mismatch') {
      const confirmed = window.confirm(t('mimoConsoleCookie.keyMismatchConfirm', {
        currentKey: details.currentKeyMask ?? '-',
        cookieKey: details.cookieKeyMask ?? '-',
      }))
      if (confirmed) {
        try {
          await bindMiMoConsoleCookie(credential, true)
          return
        } catch (adoptError) {
          form.error = adoptError instanceof Error ? adoptError.message : String(adoptError)
          return
        }
      }
    }
    form.error = err instanceof Error ? err.message : String(err)
  } finally {
    form.saving = false
  }
}

const refreshMiMoConsoleCookie = async (credential: ManagedAccountCredential) => {
  if (!props.accountUid) return
  const form = mimoForms.value[credential.credentialUid]
  form.refreshing = true
  form.error = ''
  try {
    const response = await apiService.refreshMiMoConsoleCookie(props.accountUid, credential.credentialUid)
    credential.mimoTokenPlan = response.tokenPlan
  } catch (err) {
    form.error = err instanceof Error ? err.message : String(err)
  } finally {
    form.refreshing = false
  }
}

const clearMiMoConsoleCookie = async (credential: ManagedAccountCredential) => {
  if (!props.accountUid || !window.confirm(t('mimoConsoleCookie.clearConfirm'))) return
  const form = mimoForms.value[credential.credentialUid]
  form.clearing = true
  form.error = ''
  try {
    await apiService.clearMiMoConsoleCookie(props.accountUid, credential.credentialUid)
    credential.hasMiMoConsoleCookie = false
    credential.mimoTokenPlan = undefined
  } catch (err) {
    form.error = err instanceof Error ? err.message : String(err)
  } finally {
    form.clearing = false
  }
}

const formatMiMoQuota = (quota: MiMoTokenPlanQuota) => {
  const remainingPercent = Math.max(0, Math.min(100, (1 - quota.usedPercent) * 100)).toFixed(1)
  const remaining = Math.max(0, quota.limit - quota.used)
  return `剩余 ${remainingPercent}% · ${Intl.NumberFormat().format(remaining)} tokens`
}

const planDisplayName = (plan?: string) => plan === 'agent_plan' ? 'Agent Plan' : 'Coding Plan'

interface VolcengineUsageCell {
  labelKey: string
  text: string
  colorClass: string
}

const numberFmt = new Intl.NumberFormat(undefined, { maximumFractionDigits: 1 })

const hasVolcengineUsageData = (usage?: VolcenginePlanUsage) => {
  if (!usage || usage.error) return false
  return !!(usage.fiveHour || usage.daily || usage.weekly || usage.monthly)
}

const volcengineUsageColor = quotaRemainingColorClass

// 单窗口展示：Agent Plan 显示剩余%与已用/额度，Coding Plan 显示剩余%与已用%。
const volcengineWindowCell = (labelKey: string, win?: VolcenginePlanUsageWindow): VolcengineUsageCell | null => {
  if (!win) return null
  if (typeof win.usedPercent === 'number' && Number.isFinite(win.usedPercent)) {
    const usedPercent = Math.max(0, Math.min(100, win.usedPercent))
    const remainingPercent = 100 - usedPercent
    return {
      labelKey,
      text: `${t('volcengineAccessKey.remaining')} ${remainingPercent.toFixed(1)}% · ${t('volcengineAccessKey.used')} ${usedPercent.toFixed(1)}%`,
      colorClass: volcengineUsageColor(remainingPercent),
    }
  }
  if (win.quota && win.quota > 0) {
    const remaining = Math.max(0, win.quota - win.used)
    const remainingPercent = Math.max(0, Math.min(100, (remaining / win.quota) * 100))
    return {
      labelKey,
      text: `${t('volcengineAccessKey.remaining')} ${remainingPercent.toFixed(1)}% · ${numberFmt.format(win.used)}/${numberFmt.format(win.quota)}`,
      colorClass: volcengineUsageColor(remainingPercent),
    }
  }
  return {
    labelKey,
    text: `${t('volcengineAccessKey.used')} ${numberFmt.format(win.used)}`,
    colorClass: '',
  }
}

const volcengineUsageWindows = (usage?: VolcenginePlanUsage): VolcengineUsageCell[] => {
  if (!usage) return []
  return [
    volcengineWindowCell('volcengineAccessKey.fiveHourWindow', usage.fiveHour),
    volcengineWindowCell('volcengineAccessKey.dailyWindow', usage.daily),
    volcengineWindowCell('volcengineAccessKey.weeklyWindow', usage.weekly),
    volcengineWindowCell('volcengineAccessKey.monthlyWindow', usage.monthly),
  ].filter((cell): cell is VolcengineUsageCell => cell !== null)
}

// 折叠摘要按窗口拆分为独立片段，复用 volcengineUsageColor 的阈值高亮低余量窗口。
const volcengineUsageSummaryParts = (credential: ManagedAccountCredential): VolcengineUsageCell[] => {
  if (!credential.hasVolcengineAccessKey) {
    return [{ labelKey: '', text: t('volcengineAccessKey.notConfigured'), colorClass: '' }]
  }
  if (credential.volcenginePlanUsage?.error) {
    return [{ labelKey: '', text: credential.volcenginePlanUsage.error, colorClass: '' }]
  }
  const windows = volcengineUsageWindows(credential.volcenginePlanUsage)
  if (!windows.length) {
    return [{ labelKey: '', text: t('volcengineAccessKey.noUsageData'), colorClass: '' }]
  }
  return windows.map(window => ({
    labelKey: window.labelKey,
    text: `${t(window.labelKey)} ${window.text.split(' · ')[0]}`,
    colorClass: window.colorClass,
  }))
}

const formatVolcengineTime = (iso: string): string => {
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return iso
  return date.toLocaleString()
}

const refreshVolcengineUsage = async (credential: ManagedAccountCredential) => {
  if (!props.accountUid || !credential.hasVolcengineAccessKey) return
  volcengineUsageRefreshing.value[credential.credentialUid] = true
  try {
    const response = await apiService.refreshVolcenginePlanUsage(props.accountUid, credential.credentialUid)
    credential.volcenginePlanUsage = response.usage
  } catch (err) {
    // 用量刷新失败降级为快照 error，不打断编辑框其它操作。
    credential.volcenginePlanUsage = {
      fetchedAt: new Date().toISOString(),
      error: err instanceof Error ? err.message : String(err),
    }
  } finally {
    volcengineUsageRefreshing.value[credential.credentialUid] = false
  }
}

const canSaveVolcengineCredential = (credentialUid: string) => {
  const form = volcengineForms.value[credentialUid]
  return !!form?.accessKeyId.trim() && !!form?.secretAccessKey.trim() && !form.saving
}

const saveVolcengineAccessKey = async (credential: ManagedAccountCredential) => {
  if (!props.accountUid) return
  const form = volcengineForms.value[credential.credentialUid]
  if (!form || !canSaveVolcengineCredential(credential.credentialUid)) return
  form.saving = true
  form.error = ''
  try {
    const response = await apiService.setVolcengineAccessKey(props.accountUid, credential.credentialUid, {
      accessKeyId: form.accessKeyId.trim(),
      secretAccessKey: form.secretAccessKey.trim(),
    })
    credential.hasVolcengineAccessKey = true
    credential.volcengineAccessKeyIdMask = response.accessKeyIdMask
    credential.volcenginePlan = response.plan
    credential.volcenginePlanTier = response.planTier
    credential.volcenginePlanStatus = response.planStatus
    credential.volcenginePlanUsage = response.usage
    form.accessKeyId = ''
    form.secretAccessKey = ''
  } catch (err) {
    form.error = err instanceof Error ? err.message : String(err)
  } finally {
    form.saving = false
  }
}

const clearVolcengineAccessKey = async (credential: ManagedAccountCredential) => {
  if (!props.accountUid || !window.confirm(t('volcengineAccessKey.clearConfirm'))) return
  const form = volcengineForms.value[credential.credentialUid]
  if (!form) return
  form.clearing = true
  form.error = ''
  try {
    await apiService.clearVolcengineAccessKey(props.accountUid, credential.credentialUid)
    credential.hasVolcengineAccessKey = false
    credential.volcengineAccessKeyIdMask = undefined
    credential.volcenginePlan = undefined
    credential.volcenginePlanTier = undefined
    credential.volcenginePlanStatus = undefined
    credential.volcenginePlanUsage = undefined
  } catch (err) {
    form.error = err instanceof Error ? err.message : String(err)
  } finally {
    form.clearing = false
  }
}

const saveKimiToken = async (credential: ManagedAccountCredential) => {
  if (!props.accountUid) return
  const form = kimiForms.value[credential.credentialUid]
  if (!form?.accessToken.trim()) return
  form.saving = true
  form.error = ''
  try {
    const response = await apiService.setKimiConsoleToken(props.accountUid, credential.credentialUid, form.accessToken.trim())
    credential.hasKimiConsoleToken = true
    credential.kimiCodeUsage = response.usage
    form.accessToken = ''
  } catch (err) {
    form.error = err instanceof Error ? err.message : String(err)
  } finally {
    form.saving = false
  }
}

const refreshKimiToken = async (credential: ManagedAccountCredential) => {
  if (!props.accountUid) return
  const form = kimiForms.value[credential.credentialUid]
  if (!form) return
  form.refreshing = true
  form.error = ''
  try {
    const response = await apiService.refreshKimiConsoleToken(props.accountUid, credential.credentialUid)
    credential.kimiCodeUsage = response.usage
  } catch (err) {
    form.error = err instanceof Error ? err.message : String(err)
  } finally {
    form.refreshing = false
  }
}

const clearKimiToken = async (credential: ManagedAccountCredential) => {
  if (!props.accountUid || !window.confirm(t('kimiConsoleToken.clearConfirm'))) return
  const form = kimiForms.value[credential.credentialUid]
  if (!form) return
  form.clearing = true
  form.error = ''
  try {
    await apiService.clearKimiConsoleToken(props.accountUid, credential.credentialUid)
    credential.hasKimiConsoleToken = false
    credential.kimiCodeUsage = undefined
  } catch (err) {
    form.error = err instanceof Error ? err.message : String(err)
  } finally {
    form.clearing = false
  }
}

const kimiDateTimeFormat = new Intl.DateTimeFormat(undefined, {
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
})

// 与 Kimi Code 官方 CLI 的 Plan usage 保持一致：5h limit（5 小时频限）+ Weekly limit（7 天频限），短窗口优先。
// Kimi 传入的是"已用百分比"，进度条 color 属性需要 Vuetify 色名或 hex。
const kimiUsageColor = (usedPercent: number) => {
  return quotaRemainingColorHex(100 - usedPercent)
}

const kimiFormatExpireTime = (value?: string) => {
  if (!value) return ''
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '' : kimiDateTimeFormat.format(date)
}

// priceInCents 是分，按币种格式化为带符号金额（如 CNY -> ¥）。
const kimiFormatMoney = (money: KimiCodeMoney) => {
  const amount = money.priceInCents / 100
  try {
    return new Intl.NumberFormat(undefined, {
      style: 'currency',
      currency: money.currency || 'CNY',
      minimumFractionDigits: 2,
      maximumFractionDigits: 2,
    }).format(amount)
  } catch {
    return `${amount.toFixed(2)} ${money.currency || ''}`.trim()
  }
}

const kimiFormatDateTime = (value?: string) => {
  if (!value) return '-'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '-' : kimiDateTimeFormat.format(date)
}

// 官方风格的相对重置倒计时，如 "5d 16h 29m"。
const kimiFormatCountdown = (resetTime?: string) => {
  if (!resetTime) return '-'
  const resetAt = new Date(resetTime).getTime()
  if (Number.isNaN(resetAt)) return '-'
  const totalMinutes = Math.max(0, Math.floor((resetAt - Date.now()) / 60000))
  const days = Math.floor(totalMinutes / 1440)
  const hours = Math.floor((totalMinutes % 1440) / 60)
  const minutes = totalMinutes % 60
  const parts: string[] = []
  if (days > 0) parts.push(t('kimiConsoleToken.durationDay', { value: days }))
  if (days > 0 || hours > 0) parts.push(t('kimiConsoleToken.durationHour', { value: hours }))
  parts.push(t('kimiConsoleToken.durationMinute', { value: minutes }))
  return parts.join(' ')
}

// Key 行摘要与展开明细共享固定分项顺序，并按各分项剩余量独立着色。
const kimiUsageSummaryParts = (credential: ManagedAccountCredential): VolcengineUsageCell[] => {
  if (!credential.hasKimiConsoleToken) {
    return [{ labelKey: '', text: t('kimiConsoleToken.notConfigured'), colorClass: '' }]
  }
  const usage = credential.kimiCodeUsage
  if (!usage) return [{ labelKey: '', text: t('kimiConsoleToken.noUsageData'), colorClass: '' }]

  const sections = buildKimiUsageSections(usage)
  if (!sections.length) {
    return [{ labelKey: '', text: t('kimiConsoleToken.noUsageData'), colorClass: '' }]
  }

  return sections.map(section => {
    const label = t(section.labelKey)
    if (section.kind === 'booster') {
      if (!section.wallet.allowTopup) {
        return {
          labelKey: section.labelKey,
          text: `${label} ${t('kimiConsoleToken.boosterWalletDisabled')}`,
          colorClass: '',
        }
      }
      if (section.usedPercent === undefined) {
        return {
          labelKey: section.labelKey,
          text: `${label} ${kimiFormatMoney(section.wallet.moneyLeft)} / ${kimiFormatMoney(section.wallet.moneyTotal)}`,
          colorClass: '',
        }
      }
    }
    const usedPercent = section.usedPercent
    if (usedPercent === undefined) {
      return { labelKey: '', text: t('kimiConsoleToken.noUsageData'), colorClass: '' }
    }
    const remainingPercent = Math.max(0, Math.min(100, 100 - usedPercent))
    // 摘要保持「标签 + 剩余%」的短格式，避免 Kimi/Code 拆分把行撑爆截断。
    return {
      labelKey: section.labelKey,
      text: `${label} ${t('kimiConsoleToken.percentRemaining', { percent: Math.round(remainingPercent) })}`,
      colorClass: quotaRemainingColorClass(remainingPercent),
    }
  })
}

const handleInput = () => {
  apiKeyError.value = ''
  duplicateKeyIndex.value = null
}

const handleAddKey = () => {
  const trimmed = newApiKey.value.trim()
  if (!trimmed) return

  // 检查重复
  const existingIndex = props.apiKeys.indexOf(trimmed)
  if (existingIndex !== -1) {
    apiKeyError.value = t('addChannel.duplicateKey')
    duplicateKeyIndex.value = existingIndex

    // 3秒后清除高亮
    setTimeout(() => {
      duplicateKeyIndex.value = null
    }, 3000)
    return
  }

  emit('update:apiKeys', [...props.apiKeys, trimmed])
  newApiKey.value = ''
  apiKeyError.value = ''
}

const removeKey = (index: number) => {
  const updated = props.apiKeys.filter((_, i) => i !== index)
  emit('update:apiKeys', updated)
}

// 活跃 Key 从表单移除（随保存生效）；已拉黑 Key 不在表单内，交由父组件立即调用后端删除。
const handleRemoveKey = (row: ChannelApiKeyRow) => {
  if (row.disabled) {
    emit('remove-key', row.key)
    return
  }
  removeKey(row.activeIndex)
}

const moveToTop = (index: number) => {
  const updated = [...props.apiKeys]
  const [key] = updated.splice(index, 1)
  updated.unshift(key)
  emit('update:apiKeys', updated)
}

const moveToBottom = (index: number) => {
  const updated = [...props.apiKeys]
  const [key] = updated.splice(index, 1)
  updated.push(key)
  emit('update:apiKeys', updated)
}

const clearCopilotPollTimer = () => {
  if (copilotPollTimer !== null) {
    window.clearTimeout(copilotPollTimer)
    copilotPollTimer = null
  }
}

const appendOAuthKey = (accessToken: string) => {
  if (!props.apiKeys.includes(accessToken)) {
    emit('update:apiKeys', [...props.apiKeys, accessToken])
  }
}

const oauthProxyUrl = () => props.proxyUrl?.trim() || undefined

const pollCopilotAccessToken = async (intervalSeconds: number) => {
  if (!copilotDeviceCode.value) return
  copilotPolling.value = true
  try {
    const token = await apiService.pollCopilotAccessToken(copilotDeviceCode.value, oauthProxyUrl())
    if (token.accessToken) {
      appendOAuthKey(token.accessToken)
      copilotOAuthError.value = ''
      copilotOAuthSuccess.value = true
      copilotPolling.value = false
      copilotOAuthLoading.value = false
      clearCopilotPollTimer()
      return
    }
    if (token.error === 'expired_token') {
      copilotOAuthError.value = t('copilotOAuth.expired')
      copilotOAuthSuccess.value = false
      copilotPolling.value = false
      copilotOAuthLoading.value = false
      clearCopilotPollTimer()
      return
    }
    if (token.error && token.error !== 'authorization_pending') {
      copilotOAuthError.value = token.errorDescription || token.error
      copilotOAuthSuccess.value = false
      copilotPolling.value = false
      copilotOAuthLoading.value = false
      clearCopilotPollTimer()
      return
    }
  } catch (err) {
    copilotOAuthError.value = err instanceof Error ? err.message : String(err)
    copilotOAuthSuccess.value = false
    copilotPolling.value = false
    copilotOAuthLoading.value = false
    clearCopilotPollTimer()
    return
  }

  copilotPollTimer = window.setTimeout(() => {
    void pollCopilotAccessToken(intervalSeconds)
  }, Math.max(intervalSeconds, 5) * 1000)
}

const startCopilotOAuth = async () => {
  clearCopilotPollTimer()
  copilotOAuthLoading.value = true
  copilotOAuthError.value = ''
  copilotOAuthSuccess.value = false
  try {
    const device = await apiService.requestCopilotDeviceCode(oauthProxyUrl())
    copilotDeviceCode.value = device.deviceCode
    copilotUserCode.value = device.userCode
    copilotVerificationUri.value = device.verificationUri
    window.open(device.verificationUri, '_blank', 'noopener,noreferrer')
    await pollCopilotAccessToken(device.interval || 5)
  } catch (err) {
    copilotOAuthError.value = err instanceof Error ? err.message : String(err)
    copilotOAuthLoading.value = false
    copilotPolling.value = false
  }
}

onBeforeUnmount(() => {
  clearCopilotPollTimer()
  minimaxEndpointsRequestId++
  minimaxKeyHashRequestId++
})

const cancelCopilotOAuth = () => {
  clearCopilotPollTimer()
  copilotPolling.value = false
  copilotOAuthLoading.value = false
}

const retryCopilotOAuth = () => {
  void startCopilotOAuth()
}

const diagnoseCopilotChannel = async () => {
  if (!props.channelId) return
  copilotDiagnoseLoading.value = true
  copilotDiagnoseError.value = ''
  copilotDiagnoseResult.value = null
  try {
    const latestKey = props.apiKeys[0]
    copilotDiagnoseResult.value = await apiService.diagnoseCopilotChannel(props.channelId, latestKey) as unknown as CopilotDiagnoseResponse
  } catch (err) {
    copilotDiagnoseError.value = err instanceof Error ? err.message : String(err)
  } finally {
    copilotDiagnoseLoading.value = false
  }
}

const copyKey = (key: string) => {
  navigator.clipboard.writeText(key)
  copiedKey.value = key
  setTimeout(() => {
    copiedKey.value = ''
  }, 2000)
}

const getDisabledKeyLabel = (reason: string) => {
  const map: Record<string, string> = {
    'insufficient_balance': 'channelCard.blacklistReason.insufficient_balance',
    'insufficient_quota': 'channelCard.blacklistReason.insufficient_quota',
    'unauthorized': 'channelCard.blacklistReason.authentication_error',
    'invalid': 'channelCard.blacklistReason.invalid',
    'authentication_error': 'channelCard.blacklistReason.authentication_error',
    'permission_error': 'channelCard.blacklistReason.permission_error',
  }
  return (map[reason] || 'channelCard.blacklistReason.unknown') as any
}
</script>

<style scoped>
.section-title {
  font-size: 1.125rem;
  font-weight: 600;
}

.animate-pulse {
  animation: pulse 1s ease-in-out 3;
}

@keyframes pulse {
  0%, 100% {
    opacity: 1;
  }
  50% {
    opacity: 0.6;
  }
}

/* 额度余量 5 档着色，阈值 5/10/20/30/50%，从深到浅表示余量递增。 */
.quota-critical {
  color: rgb(var(--v-theme-error)) !important;
  font-weight: 700;
}

.quota-danger {
  color: rgb(var(--v-theme-error)) !important;
}

.quota-warning {
  color: #EA580C !important;
}

.quota-caution {
  color: rgb(var(--v-theme-warning)) !important;
}

.quota-low {
  color: rgb(var(--v-theme-info)) !important;
}

.font-weight-mono {
  font-family: 'Monaco', 'Menlo', 'Ubuntu Mono', 'Courier New', monospace;
}

.volcengine-credential + .volcengine-credential {
  border-top: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
}

.volcengine-key-row {
  cursor: pointer;
}

.volcengine-key-detail {
  border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  border-top: 0;
  border-radius: 0 0 6px 6px;
}

.deepseek-credential + .deepseek-credential {
  border-top: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
}

.deepseek-balance-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(210px, 1fr));
  gap: 12px;
}

.deepseek-balance-currency {
  padding: 10px 12px;
  border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  border-radius: 6px;
}

.deepseek-balance-values {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 12px;
}

.volcengine-key-fields {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  gap: 12px;
}

.mimo-usage-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 12px;
}

.volcengine-usage-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(110px, 1fr));
  gap: 12px;
}

.kimi-plan-usage {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.kimi-plan-usage-row {
  display: grid;
  grid-template-columns: 110px minmax(120px, 1fr) auto auto;
  align-items: center;
  gap: 12px;
}

.kimi-balance-stack {
  display: flex;
  height: 6px;
  overflow: hidden;
  border-radius: 999px;
  background: rgba(var(--v-theme-on-surface), 0.08);
}

.kimi-balance-stack__kimi {
  background: #18181b;
}

.kimi-balance-stack__code {
  background: #2684ff;
}

.v-theme--dark .kimi-balance-stack__kimi {
  background: #f4f4f5;
}

.compshare-usage-grid,
.compshare-plan-meta {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 12px;
}

.compshare-usage-item {
  min-width: 0;
}

@media (max-width: 700px) {
  .volcengine-key-fields {
    grid-template-columns: minmax(0, 1fr);
  }

  .mimo-usage-grid {
    grid-template-columns: minmax(0, 1fr);
  }

  .kimi-plan-usage-row {
    grid-template-columns: 90px minmax(0, 1fr) auto;
  }

  .kimi-plan-usage-row > :last-child {
    grid-column: 1 / -1;
  }

  .compshare-usage-grid,
  .compshare-plan-meta {
    grid-template-columns: minmax(0, 1fr);
  }

  .deepseek-balance-values {
    grid-template-columns: minmax(0, 1fr);
  }
}

</style>
