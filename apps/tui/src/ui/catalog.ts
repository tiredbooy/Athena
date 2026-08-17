import type { ModelOption, ProviderPreset } from "../protocol/types.js";

export type ConnectField = "name" | "base_url" | "api_key" | "chat_model";

export type ModelPickerRow =
  | { kind: "header"; key: string; label: string }
  | { kind: "model"; key: string; option: ModelOption; index: number }
  | { kind: "connect"; key: "connect"; index: number };

const connectFields: ConnectField[] = ["name", "base_url", "api_key", "chat_model"];

export function isConnectField(value: string): value is ConnectField {
  return connectFields.includes(value as ConnectField);
}

export function fieldsFromPreset(preset: ProviderPreset): ConnectField[] {
  const advertised = preset.fields?.filter(isConnectField) ?? [];
  if (advertised.length > 0) return advertised;
  if (preset.auth !== "api_key") return [];
  const fields: ConnectField[] = [];
  if (!preset.name?.trim()) fields.push("name");
  if (!preset.base_url?.trim()) fields.push("base_url");
  fields.push("api_key");
  if (!preset.chat_model?.trim()) fields.push("chat_model");
  return fields;
}

export function nextConnectField(fields: ConnectField[], current: string): ConnectField | undefined {
  const index = fields.indexOf(current as ConnectField);
  if (index < 0 || index + 1 >= fields.length) return undefined;
  return fields[index + 1];
}

export function modelPickerRows(options: ModelOption[]): ModelPickerRow[] {
  const rows: ModelPickerRow[] = [];
  let lastProvider = "";
  options.forEach((option, index) => {
    if (option.providerName !== lastProvider) {
      lastProvider = option.providerName;
      rows.push({ kind: "header", key: `provider:${option.providerId}`, label: option.providerName });
    }
    rows.push({ kind: "model", key: `${option.providerId}:${option.model}`, option, index });
  });
  rows.push({ kind: "connect", key: "connect", index: options.length });
  return rows;
}

export function modelSelectableCount(options: ModelOption[]): number {
  return options.length + 1;
}
