import { applyDiscount } from './discount.js';
export function total(user, items) {
  return applyDiscount(user, items.reduce((a, b) => a + b, 0));
}
