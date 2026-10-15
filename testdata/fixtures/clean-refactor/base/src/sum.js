export function sum(xs) {
  let acc = 0;
  for (let i = 0; i < xs.length; i++) {
    acc = acc + xs[i];
  }
  return acc;
}
