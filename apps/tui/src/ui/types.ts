import type { Action, ModelOption, ProviderConnection, ProviderPreset } from "../protocol/types.js";
import type { ConnectField } from "./catalog.js";

export type Plan = { id: string; actions: Action[] };
export type PlanDecision = "waiting" | "applying" | "discarding";
export type Command = { name: string; description: string };
export type ConnectStep = "providers" | "remote-warning" | ConnectField | "oauth" | "saving";
export type ConnectFlow = {
  step: ConnectStep;
  presets: ProviderPreset[];
  selectedIndex: number;
  preset?: ProviderPreset;
  values: ProviderConnection;
  oauthLines: string[];
  fields: ConnectField[];
};
export type ModelFlow = { options: ModelOption[]; selectedIndex: number; selecting: boolean };
