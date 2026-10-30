# A consistent Reef interface

Keep Reef familiar across screens. These conventions guide changes to existing
screens and future additions; they are not a redesign or a new UI framework.

- **Full-size controls:** inputs, selects and adjacent toolbar/form actions use
  the shared 40px control height. Table actions, navigation and header utilities
  remain compact. Do not resize selects by changing their width to fix height.
- **Page actions:** keep Refresh visually consistent and align actions next to
  the heading. Allow wrapping on narrow screens, without clipping labels or
  forcing the entire workspace to scroll sideways. Wide tables scroll inside
  their own card.
- **Dialogs:** use a labelled heading, a clearly named close button and shared
  padding. The heading remains visible while the body scrolls. Text actions in a
  heading, such as Cancel, keep normal button typography; only close icons use
  the larger icon size. Escape retains the native dialog close behavior.
- **Feedback:** distinguish a pending read from a successfully empty result and
  an unavailable result. Do not say zero resources or invite setup until the
  initial inventory has loaded. Keep the existing loading slots and mark the
  updating list as busy for assistive technology.
- **Refresh:** preserve the reading position, filters and unsaved drafts. Retain
  the last successful data on a failed refresh and explain that it is stale.
  New host/project selections must ignore late responses from earlier selections.
- **Actions:** preserve permissions, confirmations and exact resource identity.
  Styling changes must not introduce new background mutations or deployment work.
- **Themes and keyboard:** use the existing theme tokens and visible focus state.
  Verify light/dark themes, keyboard access, narrow layouts and long dialog content
  when a browser is available. Automated state tests do not prove visual layout.

The 0.25.1 pass applies these shared sizes and dialog headings across the existing
UI, normalizes Schedules Refresh, and corrects pending/unavailable feedback in
Builds, build history/cache and Schedules. It preserves navigation and Docker
operation behavior. Agents need no upgrade for these central UI fixes.
