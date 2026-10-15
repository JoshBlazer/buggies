export function applyDiscount(user, price) {
  if (user.tier === 'Gold') return price * 0.8;
  return price;
}
