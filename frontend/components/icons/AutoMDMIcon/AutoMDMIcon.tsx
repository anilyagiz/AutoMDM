import classnames from "classnames";
import React from "react";

interface IAutoMDMIconProps {
  className?: string;
  fw?: boolean;
  name: string;
  size?: string;
  title?: string;
}

const baseClass = "automdmicon";

const AutoMDMIcon = ({
  className,
  fw,
  name,
  size,
  title,
}: IAutoMDMIconProps): JSX.Element => {
  const iconClasses = classnames(baseClass, `${baseClass}-${name}`, className, {
    [`${baseClass}-fw`]: fw,
    [`${baseClass}-${size}`]: !!size,
  });

  return <i className={iconClasses} title={title} />;
};

export default AutoMDMIcon;
