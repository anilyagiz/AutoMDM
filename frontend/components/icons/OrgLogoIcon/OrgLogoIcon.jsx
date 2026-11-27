import classnames from "classnames";
import PropTypes from "prop-types";
import React, { Component } from "react";

import automdmAvatar from "../../../assets/images/automdm-avatar-24x24@2x.png";

const baseClass = "org-logo-icon";

class OrgLogoIcon extends Component {
  static propTypes = {
    className: PropTypes.string,
    src: PropTypes.string.isRequired,
  };

  static defaultProps = {
    src: automdmAvatar,
  };

  constructor(props) {
    super(props);

    this.state = { imageSrc: props.src || automdmAvatar, prevSrc: props.src };
  }

  static getDerivedStateFromProps(nextProps, prevState) {
    if (nextProps.src !== prevState.prevSrc) {
      return {
        imageSrc: nextProps.src || automdmAvatar,
        prevSrc: nextProps.src,
      };
    }
    return null;
  }

  onError = () => {
    this.setState({ imageSrc: automdmAvatar });
  };

  render() {
    const { className } = this.props;
    const { imageSrc } = this.state;
    const { onError } = this;

    const classNames =
      imageSrc === automdmAvatar
        ? classnames(baseClass, className, "default-automdm-logo")
        : classnames(baseClass, className);

    return (
      <img
        key={imageSrc}
        alt="Organization Logo"
        className={classNames}
        onError={onError}
        src={imageSrc}
      />
    );
  }
}

export default OrgLogoIcon;
