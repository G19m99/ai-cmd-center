/**
 * Optional wrapper: a `<div>` when the host hands over a class, otherwise a
 * fragment. Lets provider bodies switch between the stacked card and the
 * row layout (meta block + window columns) without duplicating markup.
 */

import type { ReactNode } from 'react';

export interface QuotaBodyGroupProps {
  className?: string;
  children: ReactNode;
}

export function QuotaBodyGroup({ className, children }: QuotaBodyGroupProps) {
  if (!className) return <>{children}</>;
  return <div className={className}>{children}</div>;
}
