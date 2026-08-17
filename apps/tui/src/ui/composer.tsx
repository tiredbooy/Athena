import React from "react";
import { Box, Text } from "ink";
import TextInput from "ink-text-input";
import { useTheme } from "../theme.js";

export function Composer({ draft, focus, onChange, onSubmit, error, placeholder, mask }: { draft: string; focus: boolean; onChange: (value: string) => void; onSubmit: (value: string) => void; error: boolean; placeholder: string; mask?: string }): React.ReactElement {
  const theme = useTheme();
  return <Box borderStyle="single" borderColor={error ? theme.error : focus ? theme.accent : theme.muted} paddingX={1} marginTop={1}><Text color={theme.accent}>❯ </Text><TextInput value={draft} onChange={onChange} onSubmit={onSubmit} focus={focus} showCursor={focus} highlightPastedText placeholder={placeholder} mask={mask} /></Box>;
}
