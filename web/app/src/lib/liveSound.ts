// Live sound per camera (#95): muted by default, because browsers block
// autoplay with sound; a camera the viewer unmuted stays unmuted on this
// device. Storage can be missing (private mode); then nothing is remembered.
import { useEffect, useState, type RefObject } from "react";

/** Camera ids are unique only within a hive. */
const key = (hive: string, camera: string) => `camhive.live.sound.${hive}.${camera}`;

export function readSound(hive: string, camera: string): boolean {
  try {
    return localStorage.getItem(key(hive, camera)) === "on";
  } catch {
    return false;
  }
}

export function writeSound(hive: string, camera: string, on: boolean): void {
  try {
    if (on) localStorage.setItem(key(hive, camera), "on");
    else localStorage.removeItem(key(hive, camera));
  } catch {
    /* not remembered */
  }
}

/**
 * Plays with sound; when the browser refuses (no tap yet: Chrome and Safari
 * then pause the element), goes back to muted and keeps the picture playing.
 */
function playWithSound(v: HTMLVideoElement, refused: () => void): void {
  v.muted = false;
  Promise.resolve(v.play()).catch(() => {
    v.muted = true;
    refused();
    void Promise.resolve(v.play()).catch(() => undefined);
  });
}

/**
 * The sound of one live tile: `available` once the hive's audio track
 * carries media, `on` as the viewer wants it. The video plays with sound
 * only when both hold. A tap unmutes inside the click (iOS Safari allows
 * sound only there); a remembered choice is tried when audio arrives.
 */
export function useLiveSound(video: RefObject<HTMLVideoElement | null>, hive: string, camera: string) {
  const [available, setAvailable] = useState(false);
  const [on, setOn] = useState(() => readSound(hive, camera));
  const refused = () => setOn(false);
  useEffect(() => {
    const v = video.current;
    if (!v) return;
    if (on && available) playWithSound(v, refused);
    else v.muted = true;
  }, [on, available, video]); // eslint-disable-line react-hooks/exhaustive-deps -- refused only sets state
  const toggle = () => {
    writeSound(hive, camera, !on);
    setOn(!on);
    const v = video.current;
    if (v && !on && available) playWithSound(v, refused); // inside the tap
  };
  return { available, setAvailable, on, toggle };
}
